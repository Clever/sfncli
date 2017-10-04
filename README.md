# sfncli

Utility to create AWS Step Function activities out of command line programs.

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
  -version
    	Print the version and exit.
  -workername string
    	The worker name to send to AWS Step Functions when processing a task. Environment variables are expanded. The magic string MAGIC_ECS_TASK_ARN will be expanded to the ECS task ARN via the metadata service.
```

Example:

```
sfncli -activityname sleep-100 -region us-west-2 -workername sleep-worker -cmd sleep 100
```

## High-level logic

- On startup, call [`CreateActivity`](http://docs.aws.amazon.com/step-functions/latest/apireference/API_CreateActivity.html) to register an [Activity](http://docs.aws.amazon.com/step-functions/latest/dg/concepts-activities.html) with Step Functions.
- Begin polling [`GetActivityTask`](http://docs.aws.amazon.com/step-functions/latest/apireference/API_GetActivityTask.html) for tasks.
- Get a task. Take the JSON input for the task and
  - if it's a JSON object, use this as the last arg to the command.
  - if it's anything else (e.g. JSON array), an error is thown
  - if JSON object has a `_EXECUTION_NAME` property, an corresponding env var called `_EXECUTION_NAME` is add to the sub-process enviornment
- Start [`SendTaskHeartbeat`](http://docs.aws.amazon.com/step-functions/latest/apireference/API_SendTaskHeartbeat.html) loop.
- Call [`SendTaskFailure`](http://docs.aws.amazon.com/step-functions/latest/apireference/API_SendTaskFailure.html) / [`SendTaskSuccess`](http://docs.aws.amazon.com/step-functions/latest/apireference/API_SendTaskSuccess.html) when command returns.

## Local testing

Start up a test activity that runs `echo` on the work it receives.

```
go run cmd/sfncli/*.go -region us-west-2 -activityname test-activity -cmd echo
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
aws --region us-west-2 stepfunctions start-execution --state-machine-arn arn:aws:states:us-west-2:589690932525:stateMachine:test-state-machine  --input '{"hello": "world"}'
```

You should see `echo` run with the argument `{"hello": "world"}`.

## Usage

TODO
