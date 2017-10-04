package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
const maxTaskOutputLength = 32768
const maxTaskFailureCauseLength = 32768

type TaskRunner struct {
	sfnapi          sfniface.SFNAPI
	taskToken       string
	cmd             string
	logger          logger.KayveeLogger
	execCmd         *exec.Cmd
	receivedSigterm bool
}

func NewTaskRunner(cmd string, sfnapi sfniface.SFNAPI, taskToken string) TaskRunner {
	return TaskRunner{
		sfnapi:    sfnapi,
		taskToken: taskToken,
		cmd:       cmd,
		logger:    logger.New("sfncli"),
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

	// convention: if the input contains _EXECUTION_NAME, pass it to the environment of the command
	var executionName *string
	if e, ok := taskInput["_EXECUTION_NAME"].(string); ok {
		executionName = &e
		t.logger.AddContext("execution_name", *executionName)
	}

	marshaledInput, err := json.Marshal(taskInput)
	if err != nil {
		return t.sendTaskFailure(TaskFailureUnknown{fmt.Errorf("JSON input re-marshalling failed. This should never happen. %s", err)})
	}

	args = append(args, string(marshaledInput))

	t.execCmd = exec.CommandContext(ctx, t.cmd, args...)
	if executionName != nil {
		t.execCmd.Env = append(os.Environ(), "_EXECUTION_NAME="+*executionName)
	}

	// Write the stdout and stderr of the process to both this process' stdout and stderr
	// and also write to a byte buffer so that we can send the result to step functions
	stderrbuf, _ := circbuf.NewBuffer(maxTaskFailureCauseLength)
	stdoutbuf, _ := circbuf.NewBuffer(maxTaskOutputLength)
	t.execCmd.Stderr = io.MultiWriter(os.Stderr, stderrbuf)
	t.execCmd.Stdout = io.MultiWriter(os.Stdout, stdoutbuf)

	// forward signals to the command, handle SIGTERM
	go t.handleSignals(ctx)

	t.logger.InfoD("exec-command-start", logger.M{"args": args, "cmd": t.cmd})
	if err := t.execCmd.Run(); err != nil {
		stderr := strings.TrimSpace(stderrbuf.String()) // remove trailing newline
		customErrorName := parseCustomErrorNameFromStdout(stdoutbuf.String())
		if t.receivedSigterm {
			if customErrorName != "" {
				return t.sendTaskFailure(TaskFailureCustomErrorName{errorName: customErrorName, stderr: stderr})
			}
			return t.sendTaskFailure(TaskFailureCommandTerminated{stderr: stderr})
		}
		switch err := err.(type) {
		case *os.PathError:
			return t.sendTaskFailure(TaskFailureCommandNotFound{path: err.Path})
		case *exec.ExitError:
			status := err.ProcessState.Sys().(syscall.WaitStatus)
			switch {
			case status.Exited() && status.ExitStatus() > 0:
				if customErrorName != "" {
					return t.sendTaskFailure(TaskFailureCustomErrorName{errorName: customErrorName, stderr: stderr})
				}
				return t.sendTaskFailure(TaskFailureCommandExitedNonzero{stderr: stderr})
			case status.Signaled() && status.Signal() == syscall.SIGKILL:
				return t.sendTaskFailure(TaskFailureCommandKilled{stderr: stderr})
			}
		}
		return t.sendTaskFailure(TaskFailureUnknown{err})
	}
	t.logger.Info("exec-command-end")

	// AWS / states language requires JSON output
	stdout := strings.TrimSpace(stdoutbuf.String()) // remove trailing newline
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		return t.sendTaskFailure(TaskFailureCommandOutputNotJSON{stdout: stdout})
	}
	if executionName != nil {
		out["_EXECUTION_NAME"] = *executionName
	}

	marshaledOut, err := json.Marshal(out)
	if err != nil {
		return t.sendTaskFailure(TaskFailureUnknown{fmt.Errorf("JSON output re-marshalling failed. This should never happen. %s", err)})
	}
	_, err = t.sfnapi.SendTaskSuccessWithContext(ctx, &sfn.SendTaskSuccessInput{
		Output:    aws.String(string(marshaledOut)),
		TaskToken: &t.taskToken,
	})
	if err != nil {
		t.logger.ErrorD("send-task-succes-error", logger.M{"error": err.Error()})
	}

	return err
}

func (t *TaskRunner) handleSignals(ctx context.Context) {
	sigChan := make(chan os.Signal)
	signal.Notify(sigChan)
	defer signal.Stop(sigChan)
	for {
		select {
		case <-ctx.Done():
			return
		case sigReceived := <-sigChan:
			if t.execCmd.Process == nil {
				continue
			}
			pid := t.execCmd.Process.Pid
			// SIGTERM is special. If it gets sent to sfncli, initiate a docker-stop like shutdown process:
			// - forward the SIGTERM to the command
			// - after a grace period (5s) send SIGKILL to the command if it's still running
			if sigReceived == syscall.SIGTERM {
				t.receivedSigterm = true
				go func(pidtokill int) {
					time.Sleep(5 * time.Second) // grace period
					signalProcess(pidtokill, os.Signal(syscall.SIGKILL))
				}(pid)
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

func parseCustomErrorNameFromStdout(stdout string) string {
	var customError struct {
		ErrorName string `json:"error_name"`
	}
	json.Unmarshal([]byte(stdout), &customError)
	return customError.ErrorName
}
