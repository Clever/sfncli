package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/armon/circbuf"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/sfn"
	"github.com/aws/aws-sdk-go/service/sfn/sfniface"
	"gopkg.in/Clever/kayvee-go.v6/logger"
)

// stay within documented limits of SFN APIs
const (
	maxTaskOutputLength       = 32768
	maxTaskFailureCauseLength = 32768
)

// TaskRunner manages resources for executing a task
type TaskRunner struct {
	sfnapi             sfniface.SFNAPI
	taskToken          string
	cmd                string
	logger             logger.KayveeLogger
	execCmd            *exec.Cmd
	receivedSigterm    bool
	sigtermGracePeriod time.Duration
	workDirectory      string
	ctxCancel          context.CancelFunc
}

// NewTaskRunner instantiates a new TaskRunner
func NewTaskRunner(cmd string, sfnapi sfniface.SFNAPI, taskToken string, workDirectory string) TaskRunner {
	return TaskRunner{
		sfnapi:        sfnapi,
		taskToken:     taskToken,
		cmd:           cmd,
		logger:        logger.New("sfncli"),
		workDirectory: workDirectory,
		// set the default grace period to something slightly lower than the default
		// docker stop grace period in ECS (30s)
		sigtermGracePeriod: 25 * time.Second,
	}
}

// Process runs the underlying command.
// The command inherits the environment of the parent process.
// Any signals sent to parent process will be forwarded to the command.
// If the context is canceled, the command is killed.
func (t *TaskRunner) Process(ctx context.Context, args []string, input string) error {
	if t.sfnapi == nil { // if New failed :-/
		return t.sendTaskFailure(TaskFailureUnknown{errors.New("nil sfnapi")})
	}

	var taskInput map[string]interface{}
	if err := json.Unmarshal([]byte(input), &taskInput); err != nil {
		return t.sendTaskFailure(TaskFailureTaskInputNotJSON{input: input})
	}

	// _EXECUTION_NAME is a required payload parameter that we inject into the environment
	executionName, ok := taskInput["_EXECUTION_NAME"].(string)
	if !ok {
		return t.sendTaskFailure(TaskFailureTaskInputMissingExecutionName{input: input})
	}
	t.logger.AddContext("execution_name", executionName)

	marshaledInput, err := json.Marshal(taskInput)
	if err != nil {
		return t.sendTaskFailure(TaskFailureUnknown{fmt.Errorf("JSON input re-marshalling failed. This should never happen. %s", err)})
	}

	args = append(args, string(marshaledInput))

	// don't use exec.CommandContext, since we want to do graceful
	// sigterm + (grace period) + sigkill on the context finishing
	// CommandContext does sigkill immediately.
	t.execCmd = exec.Command(t.cmd, args...)
	t.execCmd.Env = append(os.Environ(), "_EXECUTION_NAME="+executionName)

	tmpDir := ""
	if t.workDirectory != "" {
		// make a new tmpDir for every run
		tmpDir, err = ioutil.TempDir(t.workDirectory, "")
		if err != nil {
			return t.sendTaskFailure(TaskFailureUnknown{fmt.Errorf("failed to create tmp dir: %s", err)})
		}

		t.execCmd.Env = append(t.execCmd.Env, fmt.Sprintf("WORK_DIR=%s", tmpDir))
		defer os.RemoveAll(tmpDir)
	}

	// Write the stdout and stderr of the process to both this process' stdout and stderr
	// and also write to a byte buffer so that we can send the result to step functions
	stderrbuf, _ := circbuf.NewBuffer(maxTaskFailureCauseLength)
	stdoutbuf, _ := circbuf.NewBuffer(maxTaskOutputLength)
	t.execCmd.Stderr = io.MultiWriter(os.Stderr, stderrbuf)
	t.execCmd.Stdout = io.MultiWriter(os.Stdout, stdoutbuf)

	// forward signals to the command, handle SIGTERM
	go t.handleSignals(ctx)

	if err := t.execCmd.Run(); err != nil {
		stderr := strings.TrimSpace(stderrbuf.String())                  // remove trailing newline
		customError, _ := parseCustomErrorFromStdout(stdoutbuf.String()) // ignore parsing errors
		if t.receivedSigterm {
			if customError.ErrorName() != "" {
				return t.sendTaskFailure(customError)
			}
			return t.sendTaskFailure(TaskFailureCommandTerminated{stderr: stderr})
		}
		switch err := err.(type) {
		case *os.PathError:
			return t.sendTaskFailure(TaskFailureCommandNotFound{path: err.Path})
		case *exec.ExitError:
			if customError.ErrorName() != "" {
				return t.sendTaskFailure(customError)
			}
			status := err.ProcessState.Sys().(syscall.WaitStatus)
			switch {
			case status.Exited() && status.ExitStatus() > 0:
				return t.sendTaskFailure(TaskFailureCommandExitedNonzero{stderr: stderr})
			case status.Signaled() && status.Signal() == syscall.SIGKILL:
				return t.sendTaskFailure(TaskFailureCommandKilled{stderr: stderr})
			}
		}
		return t.sendTaskFailure(TaskFailureUnknown{err})
	}

	// AWS / states language requires JSON output
	taskOutput := taskOutputFromStdout(stdoutbuf.String())
	var taskOutputMap map[string]interface{}
	if len(taskOutput) == 0 { // Treat "" output like {}.  Makes worker implementions easier.
		taskOutputMap = map[string]interface{}{}
	} else if err := json.Unmarshal([]byte(taskOutput), &taskOutputMap); err != nil {
		return t.sendTaskFailure(TaskFailureTaskOutputNotJSON{output: taskOutput})
	}
	// Add _EXECUTION_NAME back into the payload in case the executing worker omits the value
	// in the output.
	taskOutputMap["_EXECUTION_NAME"] = executionName

	finalTaskOutput, err := json.Marshal(taskOutputMap)
	if err != nil {
		return t.sendTaskFailure(TaskFailureUnknown{fmt.Errorf("JSON output re-marshalling failed. This should never happen. %s", err)})
	}
	_, err = t.sfnapi.SendTaskSuccessWithContext(ctx, &sfn.SendTaskSuccessInput{
		Output:    aws.String(string(finalTaskOutput)),
		TaskToken: &t.taskToken,
	})
	if err != nil {
		t.logger.ErrorD("send-task-success-error", logger.M{"error": err.Error()})
	}

	return err
}

func (t *TaskRunner) handleSignals(ctx context.Context) {
	// a buffer of one should be safe here as we're basically just catching container exits
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan)
	defer signal.Stop(sigChan)
	for {
		select {
		case <-ctx.Done():
			// if the context has ended, but the command is still running,
			// initiate graceful shutdown with a much shorter grace period,
			// since most likely this is a case of SFN timing out the
			// activity. This means there is likely another activity
			// out there beginning work on the same input.
			if t.execCmd.Process != nil && t.execCmd.ProcessState == nil {
				sigTermAndThenKill(t.execCmd.Process.Pid, 5*time.Second)
			}
			return
		case sigReceived := <-sigChan:
			if t.execCmd.Process == nil {
				continue
			}
			pid := t.execCmd.Process.Pid
			// SIGTERM is special. If it gets sent to sfncli, initiate a docker-stop like shutdown process:
			// - forward the SIGTERM to the command
			// - after a grace period send SIGKILL to the command if it's still running
			if sigReceived == syscall.SIGTERM {
				t.receivedSigterm = true
				sigTermAndThenKill(pid, t.sigtermGracePeriod)
				return
			}
			signalProcess(pid, sigReceived)
		}
		if t.receivedSigterm {
			return
		}
	}
}

func signalProcess(pid int, signal os.Signal) {
	proc := os.Process{Pid: pid}
	proc.Signal(signal)
}

// sigTermAndThenKill is a docker-stop like shutdown process:
// - send sigterm
// - after a grace period send SIGKILL if the command is still running
func sigTermAndThenKill(pid int, gracePeriod time.Duration) {
	signalProcess(pid, os.Signal(syscall.SIGTERM))
	time.Sleep(gracePeriod)
	signalProcess(pid, os.Signal(syscall.SIGKILL))
}

func parseCustomErrorFromStdout(stdout string) (TaskFailureCustom, error) {
	var customError TaskFailureCustom
	err := json.Unmarshal([]byte(taskOutputFromStdout(stdout)), &customError)
	return customError, err
}

func taskOutputFromStdout(stdout string) string {
	stdout = strings.TrimSpace(stdout) // remove trailing newline
	stdoutLines := strings.Split(stdout, "\n")
	taskOutput := ""
	if len(stdoutLines) > 0 {
		taskOutput = stdoutLines[len(stdoutLines)-1]
	}
	return taskOutput
}
