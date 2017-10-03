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
	sfnapi        sfniface.SFNAPI
	taskToken     string
	cmd           string
	args          []string
	executionName string
}

func NewTaskRunner(
	cmd string, args []string, sfnapi sfniface.SFNAPI, input, taskToken string,
) (TaskRunner, error) {
	var taskInput map[string]interface{}
	if err := json.Unmarshal([]byte(input), &taskInput); err != nil {
		return TaskRunner{}, fmt.Errorf("Input must be a json object: %s", err)
	}
	executionName, _ := taskInput["_EXECUTION_NAME"].(string)

	marshaledJob, err := json.Marshal(taskInput)
	if err != nil {
		return TaskRunner{}, err
	}

	return TaskRunner{
		sfnapi:        sfnapi,
		taskToken:     taskToken,
		executionName: executionName,
		cmd:           cmd,
		args:          append(args, string(marshaledJob)),
	}, nil
}

// Process runs the underlying cmd with the appropriate
// environment and command line params
func (t TaskRunner) Process(ctx context.Context) error {
	if t.sfnapi == nil {
		return fmt.Errorf("NewTaskFailure -- nil sfnapi") // if New failed :-/
	}
	cmd := exec.CommandContext(ctx, t.cmd, t.args...)
	cmd.Env = append(os.Environ(), "_EXECUTION_NAME="+t.executionName)

	// Write the stdout and stderr of the process to both this process' stdout and stderr
	// and also write to a byte buffer so that we can send the result to step functions
	stderrbuf, _ := circbuf.NewBuffer(maxTaskFailureCauseLength)
	stdoutbuf, _ := circbuf.NewBuffer(maxTaskOutputLength)
	cmd.Stderr = io.MultiWriter(os.Stderr, stderrbuf)
	cmd.Stdout = io.MultiWriter(os.Stdout, stdoutbuf)

	log.InfoD("exec-command-start", logger.M{"args": t.args, "cmd": t.cmd})
	if err := cmd.Run(); err != nil {
		log.InfoD("exec-command-err", logger.M{"error": err.Error()})
		if _, e := t.sfnapi.SendTaskFailureWithContext(ctx, &sfn.SendTaskFailureInput{
			Cause:     aws.String(stderrbuf.String()),
			TaskToken: &t.taskToken,
		}); e != nil {
			return fmt.Errorf("error sending task failure: %s", e)
		}
		return err
	}
	log.Info("exec-command-end")

	// AWS requires JSON output. If it isn't, then make it so.
	output := stdoutbuf.String()
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		return fmt.Errorf("Worker must output json object to stdout: %s", err)
	}
	out["_EXECUTION_NAME"] = t.executionName

	marshaledOut, err := json.Marshal(out)
	if err != nil {
		return err
	}
	_, err = t.sfnapi.SendTaskSuccessWithContext(ctx, &sfn.SendTaskSuccessInput{
		Output:    aws.String(string(marshaledOut)),
		TaskToken: &t.taskToken,
	})
	return err
}
