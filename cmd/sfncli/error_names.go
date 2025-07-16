package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"gopkg.in/Clever/kayvee-go.v6/logger"
)

// States language has the concept of "Error Names"--unique strings that correspond
// to specific error conditions under which a state can fail:
// https://states-language.net/spec.html#error-names
// SFNCLI has its own set of error names that it will report when failing a task.
// These errors are described in this file.

// TaskFailureError is the error reported when failing an activity task.
type TaskFailureError interface {
	ErrorName() string
	ErrorCause() string

	error
}

// sendTaskFailure handles sending AWS `SendTaskFailure`.
func (t TaskRunner) sendTaskFailure(err TaskFailureError) error {
	t.logger.ErrorD("send-task-failure", logger.M{"name": err.ErrorName(), "cause": err.ErrorCause()})

	// Limits from https://docs.aws.amazon.com/step-functions/latest/apireference/API_SendTaskFailure.html
	const maxErrorLength = 256
	const maxCauseLength = 32768

	_, sendErr := t.sfnapi.SendTaskFailure(
		context.Background(),
		&sfn.SendTaskFailureInput{
			Error:     aws.String(truncateString(err.ErrorName(), maxErrorLength, "[truncated]")),
			Cause:     aws.String(truncateString(err.ErrorCause(), maxCauseLength, "[truncated]")),
			TaskToken: &t.taskToken,
		},
	)
	if sendErr != nil {
		t.logger.ErrorD("send-task-failure-error", logger.M{"error": sendErr.Error()})
	}
	return err
}

// Returns its input truncated to maxLength, with the ability to replace the end to indicate truncation.
//
// For example, truncateString(s, l, "") just truncates to length l. But truncateString(s, l, "xy") will
// first truncate to length l, then replace the last two characters with "xy"
func truncateString(s string, maxLength int, truncationIndicatorSuffix string) string {
	if len(s) <= maxLength {
		return s
	}
	// when we cut out some number of bytes from the end, we may be cutting in the middle of a multi-byte unicode char
	// if so, we can use ToValidUTF8 to trim it a teeny bit further to eliminate the whole char.
	// (Note, this does mean invalid UTF8 inputs will see more changes than expected, but we won't worry about that)
	return strings.ToValidUTF8(s[:maxLength-len(truncationIndicatorSuffix)], "") + truncationIndicatorSuffix
}

// TaskFailureUnknown is used for any error that is unexpected or not understood completely.
type TaskFailureUnknown struct {
	error
}

func (t TaskFailureUnknown) ErrorName() string  { return "sfncli.Unknown" }
func (t TaskFailureUnknown) ErrorCause() string { return t.Error() }

// TaskFailureTaskInputNotJSON is used when the input to the task is not a JSON object.
type TaskFailureTaskInputNotJSON struct {
	input string
}

func (t TaskFailureTaskInputNotJSON) ErrorName() string { return "sfncli.TaskInputNotJSON" }
func (t TaskFailureTaskInputNotJSON) ErrorCause() string {
	return fmt.Sprintf("task input not valid JSON: '%s'", t.input)
}
func (t TaskFailureTaskInputNotJSON) Error() string { return t.ErrorCause() }

// TaskFailureTaskInputMissingExecutionName is used when the input to the task is not a JSON object.
type TaskFailureTaskInputMissingExecutionName struct {
	input string
}

func (t TaskFailureTaskInputMissingExecutionName) ErrorName() string {
	return "sfncli.TaskInputMissingExecutionName"
}
func (t TaskFailureTaskInputMissingExecutionName) ErrorCause() string {
	return fmt.Sprintf("task input missing _EXECUTION_NAME attribute: '%s'", t.input)
}
func (t TaskFailureTaskInputMissingExecutionName) Error() string { return t.ErrorCause() }

// TaskFailureCommandNotFound is used when the command passed to sfncli is not found.
type TaskFailureCommandNotFound struct {
	path string
}

func (t TaskFailureCommandNotFound) ErrorName() string { return "sfncli.CommandNotFound" }
func (t TaskFailureCommandNotFound) ErrorCause() string {
	return fmt.Sprintf("command not found: '%s'", t.path)
}
func (t TaskFailureCommandNotFound) Error() string { return t.ErrorCause() }

// TaskFailureCommandKilled happens when the command is sent a kill signal by the OS.
type TaskFailureCommandKilled struct {
	stderr string
}

func (t TaskFailureCommandKilled) ErrorName() string  { return "sfncli.CommandKilled" }
func (t TaskFailureCommandKilled) ErrorCause() string { return t.stderr }
func (t TaskFailureCommandKilled) Error() string {
	return fmt.Sprintf("%s: %s", t.ErrorName(), t.ErrorCause())
}

// TaskFailureCommandKilled happens when the command exits with a nonzero exit code and doesn't specifiy its own error name in the output.
type TaskFailureCommandExitedNonzero struct {
	stderr string
}

func (t TaskFailureCommandExitedNonzero) ErrorName() string  { return "sfncli.CommandExitedNonzero" }
func (t TaskFailureCommandExitedNonzero) ErrorCause() string { return t.stderr }
func (t TaskFailureCommandExitedNonzero) Error() string {
	return fmt.Sprintf("%s: %s", t.ErrorName(), t.ErrorCause())
}

// TaskFailureCustom happens when the command exits with a nonzero exit code and outputs a custom error name to stdout.
type TaskFailureCustom struct {
	Err   string `json:"error"`
	Cause string `json:"cause"`
}

func (t TaskFailureCustom) ErrorName() string  { return t.Err }
func (t TaskFailureCustom) ErrorCause() string { return t.Cause }
func (t TaskFailureCustom) Error() string {
	return fmt.Sprintf("%s: %s", t.ErrorName(), t.ErrorCause())
}

// TaskFailureTaskOutputNotJSON is used when the output of the task is not a JSON object.
type TaskFailureTaskOutputNotJSON struct {
	output string
}

func (t TaskFailureTaskOutputNotJSON) ErrorName() string { return "sfncli.TaskOutputNotJSON" }
func (t TaskFailureTaskOutputNotJSON) ErrorCause() string {
	return fmt.Sprintf("stdout not valid JSON: '%s'", t.output)
}
func (t TaskFailureTaskOutputNotJSON) Error() string { return t.ErrorCause() }

// TaskFailureCommandKilled happens when sfncli receives SIGTERM.
type TaskFailureCommandTerminated struct {
	stderr string
}

func (t TaskFailureCommandTerminated) ErrorName() string  { return "sfncli.CommandTerminated" }
func (t TaskFailureCommandTerminated) ErrorCause() string { return t.stderr }
func (t TaskFailureCommandTerminated) Error() string {
	return fmt.Sprintf("%s: %s", t.ErrorName(), t.ErrorCause())
}
