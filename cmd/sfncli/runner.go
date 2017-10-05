package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

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
	sfnapi    sfniface.SFNAPI
	taskToken string
	cmd       string
}

func NewTaskRunner(cmd string, sfnapi sfniface.SFNAPI, taskToken string) TaskRunner {
	return TaskRunner{
		sfnapi:    sfnapi,
		taskToken: taskToken,
		cmd:       cmd,
	}
}

func (t TaskRunner) handleProcessError(
	ctx context.Context, executionName, title string, err error,
) error {
	log.ErrorD(title, logger.M{"error": err.Error(), "execution_name": executionName})

	_, sendErr := t.sfnapi.SendTaskFailureWithContext(ctx, &sfn.SendTaskFailureInput{
		Cause:     aws.String(err.Error()),
		TaskToken: &t.taskToken,
	})
	if sendErr != nil {
		return fmt.Errorf("error sending task failure: %s", sendErr)
	}

	return err
}

// Process runs the underlying cmd with the appropriate
// environment and command line params
func (t TaskRunner) Process(ctx context.Context, args []string, input string) error {
	if t.sfnapi == nil { // if New failed :-/
		return t.handleProcessError(
			ctx, "process-init", "unknown", fmt.Errorf("NewTaskFailure -- nil sfnapi"),
		)
	}

	var taskInput map[string]interface{}
	if err := json.Unmarshal([]byte(input), &taskInput); err != nil {
		return t.handleProcessError(
			ctx, "process-input", "unknown", fmt.Errorf("Input must be a json object: %s", err),
		)
	}
	executionName, _ := taskInput["_EXECUTION_NAME"].(string)

	marshaledInput, err := json.Marshal(taskInput)
	if err != nil {
		return t.handleProcessError(ctx, "process-input", executionName, err)
	}

	args = append(args, string(marshaledInput))

	cmd := exec.CommandContext(ctx, t.cmd, args...)
	cmd.Env = append(os.Environ(), "_EXECUTION_NAME="+executionName)

	// Write the stdout and stderr of the process to both this process' stdout and stderr
	// and also write to a byte buffer so that we can send the result to step functions
	stderrbuf, _ := circbuf.NewBuffer(maxTaskFailureCauseLength)
	stdoutbuf, _ := circbuf.NewBuffer(maxTaskOutputLength)
	cmd.Stderr = io.MultiWriter(os.Stderr, stderrbuf)
	cmd.Stdout = io.MultiWriter(os.Stdout, stdoutbuf)

	log.InfoD("exec-command-start", logger.M{
		"args": args, "cmd": t.cmd, "execution_name": executionName,
	})
	if err := cmd.Run(); err != nil {
		return t.handleProcessError(ctx, "exec-command-err", executionName, err)
	}
	log.InfoD("exec-command-end", logger.M{"execution_name": executionName})

	// AWS requires JSON output. If it isn't, then make it so.
	output := stdoutbuf.String()
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		wrappedErr := fmt.Errorf("Worker must output json object to stdout: %s", err)
		return t.handleProcessError(ctx, "process-output", executionName, wrappedErr)
	}
	out["_EXECUTION_NAME"] = executionName

	marshaledOut, err := json.Marshal(out)
	if err != nil {
		return t.handleProcessError(ctx, "process-output", executionName, err)
	}
	_, err = t.sfnapi.SendTaskSuccessWithContext(ctx, &sfn.SendTaskSuccessInput{
		Output:    aws.String(string(marshaledOut)),
		TaskToken: &t.taskToken,
	})
	if err != nil {
		log.ErrorD(
			"process-success", logger.M{"error": err.Error(), "execution_name": executionName},
		)
	}

	return err
}
