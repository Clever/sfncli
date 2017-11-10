package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sfn"
	"github.com/aws/aws-sdk-go/service/sfn/sfniface"
	"gopkg.in/Clever/kayvee-go.v6/logger"
)

var log = logger.New("sfncli")

// Version denotes the version of sfncli. A value is injected at compilation via ldflags
var Version string

func main() {
	activityName := flag.String("activityname", "", "The activity name to register with AWS Step Functions. $VAR and ${VAR} env variables are expanded.")
	workerName := flag.String("workername", "", "The worker name to send to AWS Step Functions when processing a task. Environment variables are expanded. The magic string MAGIC_ECS_TASK_ARN will be expanded to the ECS task ARN via the metadata service.")
	cmd := flag.String("cmd", "", "The command to run to process activity tasks.")
	region := flag.String("region", "", "The AWS region to send Step Function API calls. Defaults to AWS_REGION.")
	workDirectory := flag.String("workdirectory", "", "Create the specified directory pass the path using the environment variable WORK_DIR to the cmd processing a task. Default is to not create the path.")
	printVersion := flag.Bool("version", false, "Print the version and exit.")

	flag.Parse()

	if *printVersion {
		fmt.Println(Version)
		os.Exit(0)
	}

	if *activityName == "" {
		fmt.Println("activityname is required")
		os.Exit(1)
	}
	*activityName = os.ExpandEnv(*activityName)
	if *workerName == "" {
		fmt.Println("workername is required")
		os.Exit(1)
	}
	*workerName = os.ExpandEnv(*workerName)
	if newWorkerName, err := expandECSTaskARN(*workerName); err != nil {
		fmt.Printf("error expanding %s: %s", magicECSTaskARN, err)
		os.Exit(1)
	} else {
		*workerName = newWorkerName
	}
	if *cmd == "" {
		fmt.Println("cmd is required")
		os.Exit(1)
	}
	if *region == "" {
		*region = os.Getenv("AWS_REGION")
		if *region == "" {
			fmt.Println("region or AWS_REGION is required")
			os.Exit(1)
		}
	}
	if *workDirectory != "" {
		if err := validateWorkDirectory(*workDirectory); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		defer os.RemoveAll(*workDirectory)
	}

	mainCtx, mainCtxCancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Signal(syscall.SIGTERM))
	go func() {
		for _ = range c {
			// sig is a ^C, handle it
			mainCtxCancel()
		}
	}()

	// register the activity with AWS (it might already exist, which is ok)
	sfnapi := sfn.New(session.New(), aws.NewConfig().WithRegion(*region))
	createOutput, err := sfnapi.CreateActivityWithContext(mainCtx, &sfn.CreateActivityInput{
		Name: activityName,
	})
	if err != nil {
		fmt.Printf("error creating activity: %s\n", err)
		os.Exit(1)
	}
	log.InfoD("startup", logger.M{"activity": *createOutput.ActivityArn, "worker-name": *workerName})

	// run getactivitytask and get some work
	// getactivitytask claims to initiate a polling loop, but it seems to return every few minutes with
	// a nil error and empty output. So wrap it in a polling loop of our own
	ticker := time.NewTicker(5 * time.Second)
	for mainCtx.Err() == nil {
		select {
		case <-mainCtx.Done():
			log.Info("getactivitytask-stop")
			continue
		case <-ticker.C:
			log.InfoD("getactivitytask-start", logger.M{"activity-arn": *createOutput.ActivityArn, "worker-name": *workerName})
			getATOutput, err := sfnapi.GetActivityTaskWithContext(mainCtx, &sfn.GetActivityTaskInput{
				ActivityArn: createOutput.ActivityArn,
				WorkerName:  workerName,
			})
			if err != nil {
				log.ErrorD("getactivitytask-error", logger.M{"error": err.Error()})
				continue
			}
			if getATOutput.TaskToken == nil {
				continue
			}
			input := *getATOutput.Input
			token := *getATOutput.TaskToken
			log.InfoD("getactivitytask", logger.M{"input": input, "token": token})

			// Create a context for this task. We'll cancel this context on errors.
			// Anything spawned on behalf of the task should use this context.
			var taskCtx context.Context
			var taskCtxCancel context.CancelFunc
			taskCtx, taskCtxCancel = context.WithCancel(mainCtx)

			// Begin sending heartbeats
			go func() {
				if err := taskHeartbeat(taskCtx, sfnapi, token); err != nil {
					log.ErrorD("heartbeat-error", logger.M{"error": err.Error()})
					taskCtxCancel() // if the heartbeat has an error, shut down the task
					return
				}
				log.InfoD("heartbeat-end", logger.M{"token": token})
			}()

			// Run the command. Treat unprocessed args (flag.Args()) as additional args to
			// send to the command on every invocation of the command
			taskRunner := NewTaskRunner(*cmd, sfnapi, token, *workDirectory)
			err = taskRunner.Process(taskCtx, flag.Args(), input)
			if err != nil {
				taskCtxCancel()
				continue
			}

			// success!
			taskCtxCancel()
		}
	}
}

// validateWorkDirectory ensures the directory exists and is writable
func validateWorkDirectory(dirname string) error {
	dirInfo, err := os.Stat(dirname)

	// does not exist; create dir
	if os.IsNotExist(err) {
		fmt.Printf("creating dirname %s\n", dirname)
		if err := os.MkdirAll(dirname, os.ModeTemporary|0700); err != nil {
			return fmt.Errorf("workDirectory create error: %s", err)
		}

		return nil
	}

	// dir exists; ensure permissions and mode
	if !dirInfo.IsDir() {
		return fmt.Errorf("workDirectory is not a directory")
	}
	if _, err := ioutil.TempFile(dirname, ""); err != nil {
		return fmt.Errorf("workDirectory write error: %s", err)
	}

	return nil
}

func taskHeartbeat(ctx context.Context, sfnapi sfniface.SFNAPI, token string) error {
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeat.C:
			if _, err := sfnapi.SendTaskHeartbeatWithContext(ctx, &sfn.SendTaskHeartbeatInput{
				TaskToken: aws.String(token),
			}); err != nil {
				if aerr, ok := err.(awserr.Error); ok {
					switch aerr.Code() {
					case sfn.ErrCodeInvalidToken, sfn.ErrCodeTaskDoesNotExist, sfn.ErrCodeTaskTimedOut:
						return err
					}
				}
				log.ErrorD("heartbeat-error-unknown", logger.M{"error": err.Error()}) // keep trying on unknown errors
			}
		}
	}
}
