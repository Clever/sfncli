# sfncli

Utility to create AWS Step Function (SFN) activities out of command line programs.

## Usage

```
$ sfncli -h
Usage of sfncli:
  -activityname string
    	The activity name to register with AWS Step Functions. $VAR and ${VAR} env variables are expanded.
  -cmd string
    	The command to run to process activity tasks.
  -region string
    	The AWS region to send Step Function API calls. Defaults to AWS_REGION.
  -cloudwatchregion string
      The AWS region to send metric data. Defaults to the value of region.
  -version
    	Print the version and exit.
  -workername string
    	The worker name to send to AWS Step Functions when processing a task. Environment variables are expanded. The magic string MAGIC_ECS_TASK_ARN will be expanded to the ECS task ARN via the metadata service.
  -workdirectory string
    	A directory path that is passed to the `cmd` using an env var `WORK_DIR`. For each activity task a new directory is created in `workdirectory` and it is cleaned up after the activity task exits. Defaults to "", does not create directory or set `WORK_DIR`
  -inputfile
    	Write task input to a file (input.json) in WORK_DIR instead of passing as CLI argument. Useful for avoiding ARG_MAX limits with large payloads. Requires -workdirectory to be set.
```

Example:

```
sfncli -activityname sleep-100 -region us-west-2 --cloudwatchregion us-west-1 -workername sleep-worker -cmd sleep 100
```

## High-level logic

- On startup, call [`CreateActivity`](http://docs.aws.amazon.com/step-functions/latest/apireference/API_CreateActivity.html) to register an [Activity](http://docs.aws.amazon.com/step-functions/latest/dg/concepts-activities.html) with Step Functions.
- Begin polling [`GetActivityTask`](http://docs.aws.amazon.com/step-functions/latest/apireference/API_GetActivityTask.html) for tasks.
- Get a task. Take the JSON input for the task and
  - if it's a JSON object, use this as the last arg to the `cmd` passed to `sfncli` (unless `-inputfile` is set, in which case write it to `WORK_DIR/input.json` and pass the file path as the argument).
  - if it's anything else (e.g. JSON array), an error is thrown.
  - if `_EXECUTION_NAME` is missing from the payload, an error is thrown
  - the `_EXECUTION_NAME` payload attribute value is added to the environment of the `cmd` as `_EXECUTION_NAME`.
  - if workdirectory is set, create a sub-directory and add it to the environment of the `cmd` as `WORK_DIR`.
- Start [`SendTaskHeartbeat`](http://docs.aws.amazon.com/step-functions/latest/apireference/API_SendTaskHeartbeat.html) loop.
- When the command exits:
  - Call [`SendTaskFailure`](http://docs.aws.amazon.com/step-functions/latest/apireference/API_SendTaskFailure.html) if it exited nonzero, was killed, or `sfncli` received SIGTERM.
  - Call [`SendTaskSuccess`](http://docs.aws.amazon.com/step-functions/latest/apireference/API_SendTaskSuccess.html) otherwise.
    Parse the last line of the `stdout` of the command as the output for the task (it [must be JSON](https://states-language.net/spec.html#data)).
  - If `workdirectory` was set then cleanup `WORK_DIR`/sub-directory-for-task

## Errors

[Error names](https://states-language.net/spec.html#error-names) in SFN state machines are useful for debugging and setting up branching/retry logic in state machine definitions.
`sfncli` will report the following error names if it encounters errors it can identify:

- `sfncli.TaskInputNotJSON`: input to the task was not JSON
- `sfncli.TaskFailureTaskInputMissingExecutionName`: input is missing `_EXECUTION_NAME` attribute
- `sfncli.CommandNotFound`: the command passed to `sfncli` was not found
- `sfncli.CommandKilled`: the command process received SIGKILL
- `sfncli.CommandExitedNonzero`: the command process exited with a nonzero exit code
- `sfncli.TaskOutputNotJSON`: the task output (last line of command's `stdout`) was not JSON
- `sfncli.CommandTerminated`: `sfncli` or the command received SIGTERM
- `sfncli.Unknown`: unexpected / unclassified errors

The command should signal an error by exiting with a nonzero status code. In this case, the behavior is:
1. If the last line of *stdout* was a JSON-formatted string with an `error` field, report an error to Step Functions with that field as the name and the value of the `cause` field in the output line as the cause.
2. Otherwise, report an error with name `sfncli.CommandExitedNonzero` with the last line of *stderr* as the cause.

## Local testing

Start up a test activity that runs `echo` on the work it receives.

```
go run ./cmd/sfncli -region us-west-2 -activityname test-activity -workername sfncli-test -cmd echo
```

Create a new state machine that uses this activity for one of its states (this requires you to [create a role for use with Step Functions](http://docs.aws.amazon.com/step-functions/latest/dg/procedure-create-iam-role.html)):

```
aws --region us-west-2 stepfunctions create-state-machine --name test-state-machine --role-arn arn:aws:iam::589690932525:role/raf-test-step-functions --definition '{
    "Comment": "Testing out step functions",
    "StartAt": "foo",
    "Version": "1.0",
    "TimeoutSeconds": 60,
    "States": {
        "foo": {
            "Resource": "arn:aws:states:us-west-2:589690932525:activity:test-activity",
            "Type": "Task",
            "End": true
        }
    }
}'
```

Note that you will need to replace the `Resource` above to reflect the correct ARN with your AWS account ID.

Start an execution of the state machine (again replacing the ARN below with the correct account ID):

```
aws --region us-west-2 stepfunctions start-execution --state-machine-arn arn:aws:states:us-west-2:589690932525:stateMachine:test-state-machine  --input '{"_EXECUTION_NAME":"en", "hello": "world"}'
```

You should see `echo` run with the argument `{"hello": "world"}`.
