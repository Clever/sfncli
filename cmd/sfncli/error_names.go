package main

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/sfn"
	"gopkg.in/Clever/kayvee-go.v6/logger"
)

// States language has the concept of "Error Names"--unique strings that correspond
// to specific error conditions under which a state can fail:
// https://states-language.net/spec.html#error-names
// SFNCLI has its own set of error names that it will report when failing a task.
// These errors are described in this file.

// TaskFailureError is the error reported when failing an activity task.
type TaskFailureError interface {
	error
	ErrorName() string
}

// sendTaskFailure handles sending AWS `SendTaskFailure`.
func (t TaskRunner) sendTaskFailure(err TaskFailureError) error {
	t.logger.ErrorD("send-task-failure", logger.M{"name": err.ErrorName(), "error": err.Error()})

	// don't use SendTaskFailureWithContext, since the failure itself could be from the parent
	// context being cancelled, but we still want to report to AWS the failure of the task.
	_, sendErr := t.sfnapi.SendTaskFailure(&sfn.SendTaskFailureInput{
		Cause:     aws.String(err.Error()),
		Error:     aws.String(err.ErrorName()),
		TaskToken: &t.taskToken,
	})
	if sendErr != nil {
		t.logger.ErrorD("send-task-failure-error", logger.M{"error": sendErr.Error()})
	}
	return err
}

// TaskFailureUnknown is used for any error that is unexpected or not understood completely.
type TaskFailureUnknown struct {
	error
}

func (t TaskFailureUnknown) ErrorName() string { return "sfncli.Unknown" }

// TaskFailureTaskInputNotJSON is used when the input to the task is not a JSON object.
type TaskFailureTaskInputNotJSON struct {
	input string
}

func (t TaskFailureTaskInputNotJSON) Error() string {
	return fmt.Sprintf("task input not valid JSON: '%s'", t.input)
}

func (t TaskFailureTaskInputNotJSON) ErrorName() string { return "sfncli.TaskInputNotJSON" }

// TaskFailureCommandNotFound is used when the command passed to sfncli is not found.
type TaskFailureCommandNotFound struct {
	path string
}

func (t TaskFailureCommandNotFound) Error() string {
	return fmt.Sprintf("command not found: '%s'", t.path)
}

func (t TaskFailureCommandNotFound) ErrorName() string { return "sfncli.CommandNotFound" }

// TaskFailureCommandKilled happens when the command is sent a kill signal by the OS.
type TaskFailureCommandKilled struct {
	stderr string
}

func (t TaskFailureCommandKilled) Error() string { return t.stderr }

func (t TaskFailureCommandKilled) ErrorName() string { return "sfncli.CommandKilled" }

// TaskFailureCommandKilled happens when the command exits with a nonzero exit code and doesn't specifiy its own error name in the output.
type TaskFailureCommandExitedNonzero struct {
	stderr string
}

func (t TaskFailureCommandExitedNonzero) Error() string { return t.stderr }

func (t TaskFailureCommandExitedNonzero) ErrorName() string { return "sfncli.CommandExitedNonzero" }

// TaskFailureCustomErrorName happens when the command exits with a nonzero exit code and outputs a custom error name to stdout.
type TaskFailureCustomErrorName struct {
	errorName string
	stderr    string
}

func (t TaskFailureCustomErrorName) Error() string { return t.stderr }

func (t TaskFailureCustomErrorName) ErrorName() string { return t.errorName }

// TaskFailureTaskOutputNotJSON is used when the output of the task is not a JSON object.
type TaskFailureTaskOutputNotJSON struct {
	output string
}

func (t TaskFailureTaskOutputNotJSON) Error() string {
	return fmt.Sprintf("stdout not valid JSON: '%s'", t.output)
}

func (t TaskFailureTaskOutputNotJSON) ErrorName() string { return "sfncli.TaskOutputNotJSON" }

// TaskFailureCommandKilled happens when sfncli receives SIGTERM.
type TaskFailureCommandTerminated struct {
	stderr string
}

func (t TaskFailureCommandTerminated) Error() string { return t.stderr }

func (t TaskFailureCommandTerminated) ErrorName() string { return "sfncli.CommandTerminated" }
