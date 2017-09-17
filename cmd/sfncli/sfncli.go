package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sfn"
	"github.com/aws/aws-sdk-go/service/sfn/sfniface"
	"gopkg.in/Clever/kayvee-go.v6/logger"
)

var log = logger.New("sfncli")

var Version string

func main() {
	name := flag.String("name", "", "The activity name to register with AWS Step Functions.")
	cmd := flag.String("cmd", "", "The command to run to process activity tasks.")
	region := flag.String("region", "", "The AWS region to send Step Function API calls. Defaults to AWS_REGION.")
	printVersion := flag.Bool("version", false, "Print the version and exit.")

	flag.Parse()

	if *printVersion {
		fmt.Println(Version)
		os.Exit(0)
	}

	if *name == "" {
		fmt.Println("name is required")
		os.Exit(1)
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

	// TODO: set workerName to something useful
	workerName := "worker-name"

	mainCtx, mainCtxCancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for _ = range c {
			// sig is a ^C, handle it
			mainCtxCancel()
		}
	}()

	// register the activity with AWS (it might already exist, which is ok)
	sfnapi := sfn.New(session.New(), aws.NewConfig().WithRegion(*region))
	createOutput, err := sfnapi.CreateActivityWithContext(mainCtx, &sfn.CreateActivityInput{
		Name: name,
	})
	if err != nil {
		fmt.Printf("error creating activity: %s\n", err)
		os.Exit(1)
	}
	log.InfoD("startup", logger.M{"activity": *createOutput.ActivityArn, "worker-name": workerName})

	// run getactivitytask and get some work
	// getactivitytask itself claims to initiate a polling loop, but wrap it in a polling loop of our own
	// since it seems to return every minute or so with a nil error and empty output
	ticker := time.NewTicker(5 * time.Second)
OuterLoop:
	for {
		select {
		case <-mainCtx.Done():
			log.Info("getactivitytask-stop")
			break OuterLoop // :-/ https://golang.org/ref/spec#Break_statements
		case <-ticker.C:
			getATOutput, err := sfnapi.GetActivityTaskWithContext(mainCtx, &sfn.GetActivityTaskInput{
				ActivityArn: createOutput.ActivityArn,
				WorkerName:  &workerName,
			})
			if err != nil {
				log.ErrorD("getactivitytask-error", logger.M{"error": err.Error()})
				continue
			}
			if getATOutput.TaskToken == nil {
				log.Info("getactivitytask-restart")
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

			// Run the command
			taskRunner := NewTaskRunner(*cmd, flag.Args(), sfnapi, input, token)
			if err := taskRunner.Process(taskCtx); err != nil {
				log.ErrorD("process-error", logger.M{"error": err.Error()})
				taskCtxCancel()
				continue
			}

			// success!
			taskCtxCancel()
		}
	}
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
