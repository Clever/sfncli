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
	args      []string
}

func NewTaskRunner(cmd string, args []string, sfnapi sfniface.SFNAPI, taskInput string, taskToken string) TaskRunner {
	// if the task input is an array of strings, interpret these as an args array
	// otherwise pass the raw input as a single arg
	var taskInputArgs []string
	if err := json.Unmarshal([]byte(taskInput), &taskInputArgs); err != nil {
		taskInputArgs = []string{taskInput}
	}

	return TaskRunner{
		sfnapi:    sfnapi,
		taskToken: taskToken,
		cmd:       cmd,
		args:      append(args, taskInputArgs...),
	}
}

// Process runs the underlying cmd with the appropriate
// environment and command line params
func (t TaskRunner) Process(ctx context.Context) error {
	if t.sfnapi == nil {
		return nil // if New failed :-/
	}
	cmd := exec.CommandContext(ctx, t.cmd, t.args...)
	cmd.Env = os.Environ()

	// Write the stdout and stderr of the process to both this process' stdout and stderr
	// and also write to a byte buffer so that we can send the result to step functions
	stderrbuf, _ := circbuf.NewBuffer(maxTaskFailureCauseLength)
	stdoutbuf, _ := circbuf.NewBuffer(maxTaskOutputLength)
	cmd.Stderr = io.MultiWriter(os.Stderr, stderrbuf)
	cmd.Stdout = io.MultiWriter(os.Stdout, stdoutbuf)

	log.InfoD("exec-command-start", map[string]interface{}{
		"args": t.args,
		"cmd":  t.cmd,
	})
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
	var test interface{}
	if err := json.Unmarshal([]byte(output), &test); err != nil {
		// output isn't JSON, make it json and stay under the length limit
		if len(output)+100 > maxTaskOutputLength && len(output) > 100 {
			output = output[100:] // stay under the limit
		}
		newOutputBs, _ := json.Marshal(map[string]interface{}{
			"raw": output,
		})
		output = string(newOutputBs)
	}

	_, err := t.sfnapi.SendTaskSuccessWithContext(ctx, &sfn.SendTaskSuccessInput{
		Output:    aws.String(output),
		TaskToken: &t.taskToken,
	})
	return err
}
