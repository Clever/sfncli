package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Clever/kayvee-go/v7/logger"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/aws/smithy-go"
	"golang.org/x/time/rate"
)

var log = logger.New("sfncli")

// Version denotes the version of sfncli. A value is injected at compilation via ldflags
var Version string

func main() {
	activityName := flag.String("activityname", "", "The activity name to register with AWS Step Functions. $VAR and ${VAR} env variables are expanded.")
	workerName := flag.String("workername", "", "The worker name to send to AWS Step Functions when processing a task. Environment variables are expanded. The magic string MAGIC_ECS_TASK_ARN will be expanded to the ECS task ARN via the metadata service.")
	cmd := flag.String("cmd", "", "The command to run to process activity tasks.")
	region := flag.String("region", "", "The AWS region to send Step Function API calls. Defaults to AWS_REGION.")
	cloudWatchRegion := flag.String("cloudwatchregion", "", "The AWS region to report metrics. Defaults to the value of the region flag.")
	workDirectory := flag.String("workdirectory", "", "Create the specified directory pass the path using the environment variable WORK_DIR to the cmd processing a task. Default is to not create the path.")
	inputFile := flag.Bool("inputfile", false, "Write task input to a file (input.json) in WORK_DIR instead of passing as CLI argument. Useful for avoiding ARG_MAX limits with large payloads.")
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
	if newWorkerName, err := expandECSMagicStrings(*workerName); err != nil {
		fmt.Printf("error expanding %s: %s", magicECSTaskARN, err)
		os.Exit(1)
	} else {
		*workerName = newWorkerName
	}

	if *cmd == "" {
		fmt.Println("cmd is required")
		os.Exit(1)
	}
	*cmd = os.ExpandEnv(*cmd) // Allow environment variable substition in the cmd flag.

	if *region == "" {
		*region = os.Getenv("AWS_REGION")
		if *region == "" {
			fmt.Println("region or AWS_REGION is required")
			os.Exit(1)
		}
	}
	if *cloudWatchRegion == "" {
		*cloudWatchRegion = *region
	} else {
		*cloudWatchRegion = os.ExpandEnv(*cloudWatchRegion)
	}

	if *workDirectory != "" {
		if err := validateWorkDirectory(*workDirectory); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	mainCtx, mainCtxCancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Signal(syscall.SIGTERM))
	go func() {
		for range c {
			// sig is a ^C, handle it
			mainCtxCancel()
		}
	}()

	// Load AWS config
	cfg, err := config.LoadDefaultConfig(mainCtx, config.WithRegion(*region))
	if err != nil {
		fmt.Printf("error loading AWS config: %s\n", err)
		os.Exit(1)
	}

	// register the activity with AWS (it might already exist, which is ok)
	activityTags := tagsFromEnv()
	sfnapi := sfn.NewFromConfig(cfg)
	createOutput, err := sfnapi.CreateActivity(mainCtx, &sfn.CreateActivityInput{
		Name: aws.String(*activityName),
		Tags: activityTags,
	})
	if err != nil {
		fmt.Printf("error creating activity: %s\n", err)
		os.Exit(1)
	}

	// if the activity already exists, tags won't be applied, so explicitly
	// set tags here
	if _, err := sfnapi.TagResource(mainCtx, &sfn.TagResourceInput{
		ResourceArn: createOutput.ActivityArn,
		Tags:        activityTags,
	}); err != nil {
		fmt.Printf("error tagging activity: %s\n", err)
		os.Exit(1)
	}

	log.InfoD("startup", logger.M{
		"activity":       *createOutput.ActivityArn,
		"worker-name":    *workerName,
		"work-directory": *workDirectory,
	})

	// set up cloudwatch metric reporting
	cwcfg, err := config.LoadDefaultConfig(mainCtx, config.WithRegion(*cloudWatchRegion))
	if err != nil {
		fmt.Printf("error loading CloudWatch config: %s\n", err)
		os.Exit(1)
	}
	cwapi := cloudwatch.NewFromConfig(cwcfg)
	cw := NewCloudWatchReporter(cwapi, *createOutput.ActivityArn)
	go cw.ReportActivePercent(mainCtx, 60*time.Second)
	cw.SetActiveState(true)

	// allow one GetActivityTask per second, max 1 at a time
	limiter := rate.NewLimiter(rate.Every(1*time.Second), 1)

	// run getactivitytask and get some work
	// getactivitytask claims to initiate a polling loop, but it seems to return every few minutes with
	// a nil error and empty output. So wrap it in a polling loop of our own
	for mainCtx.Err() == nil {
		select {
		case <-mainCtx.Done():
			log.Info("getactivitytask-stop")
		default:
			cw.SetActiveState(false)
			// setting paused here so the time spent waiting for the limiter is not counted as time
			// the task is inactive in the activePercent calculation
			cw.SetPausedState(true)
			if err := limiter.Wait(mainCtx); err != nil {
				// must unpause here because no longer waiting for limiter
				cw.SetPausedState(false)
				continue
			}
			// must unpaused here because no longer waiting for limiter
			cw.SetPausedState(false)

			log.TraceD("getactivitytask-start", logger.M{
				"activity-arn": *createOutput.ActivityArn, "worker-name": *workerName,
			})
			getATOutput, err := sfnapi.GetActivityTask(mainCtx, &sfn.GetActivityTaskInput{
				ActivityArn: createOutput.ActivityArn,
				WorkerName:  aws.String(*workerName),
			})
			if err != nil {
				// if the context is canceled or request is canceled, we can continue
				if err == context.Canceled {
					log.Warn("getactivitytask-cancel")
					continue
				}
				var opErr *smithy.OperationError
				if errors.As(err, &opErr) && opErr.Err.Error() == "request canceled" {
					log.Warn("getactivitytask-cancel")
					continue
				}
				log.ErrorD("getactivitytask-error", logger.M{"error": err.Error()})
				continue
			}
			if getATOutput.TaskToken == nil { // No jobs to do
				log.Debug("getactivitytask-skip")
				continue
			}

			cw.SetActiveState(true)
			input := *getATOutput.Input
			token := *getATOutput.TaskToken
			log.TraceD("getactivitytask", logger.M{"input": input, "token": token})

			// Create a context for this task. We'll cancel this context on errors.
			taskCtx, taskCtxCancel := context.WithCancel(mainCtx)

			// Begin sending heartbeats
			go func() {
				if err := taskHeartbeatLoop(taskCtx, sfnapi, token); err != nil {
					log.ErrorD("heartbeat-error", logger.M{"error": err.Error()})
					// taskHeartBeatLoop only returns errors when they should be treated as critical
					// e.g., if the task timed out
					// shut down the command in these cases
					taskCtxCancel()
					return
				}
				log.TraceD("heartbeat-end", logger.M{"token": token})
			}()

			// Run the command. Treat unprocessed args (flag.Args()) as additional args to
			// send to the command on every invocation of the command
			taskRunner := NewTaskRunner(*cmd, sfnapi, token, *workDirectory, *inputFile)
			err = taskRunner.Process(taskCtx, flag.Args(), input)
			if err != nil {
				log.ErrorD("task-process-error", logger.M{"error": err.Error()})
				taskCtxCancel()
				continue
			}

			// success!
			taskCtxCancel()
		}
	}
}

func tagsFromEnv() []types.Tag {
	tags := []types.Tag{}
	if env := os.Getenv("_DEPLOY_ENV"); env != "" {
		tags = append(tags, types.Tag{Key: aws.String("environment"), Value: aws.String(env)})
	}
	if app := os.Getenv("_APP_NAME"); app != "" {
		tags = append(tags, types.Tag{Key: aws.String("application"), Value: aws.String(app)})
	}
	if pod := os.Getenv("_POD_ID"); pod != "" {
		tags = append(tags, types.Tag{Key: aws.String("pod"), Value: aws.String(pod)})
	}
	if shortname := os.Getenv("_POD_SHORTNAME"); shortname != "" {
		tags = append(tags, types.Tag{Key: aws.String("pod-shortname"), Value: aws.String(shortname)})
	}
	if region := os.Getenv("_POD_REGION"); region != "" {
		tags = append(tags, types.Tag{Key: aws.String("pod-region"), Value: aws.String(region)})
	}
	if account := os.Getenv("_POD_ACCOUNT"); account != "" {
		tags = append(tags, types.Tag{Key: aws.String("pod-account"), Value: aws.String(account)})
	}
	if team := os.Getenv("_TEAM_OWNER"); team != "" {
		tags = append(tags, types.Tag{Key: aws.String("team"), Value: aws.String(team)})
	}

	return tags
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

func taskHeartbeatLoop(ctx context.Context, sfnapi SFNAPI, token string) error {
	if err := sendTaskHeartbeat(ctx, sfnapi, token); err != nil {
		return err
	}
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeat.C:
			if err := sendTaskHeartbeat(ctx, sfnapi, token); err != nil {
				return err
			}
		}
	}
}

func sendTaskHeartbeat(ctx context.Context, sfnapi SFNAPI, token string) error {
	if _, err := sfnapi.SendTaskHeartbeat(ctx, &sfn.SendTaskHeartbeatInput{
		TaskToken: aws.String(token),
	}); err != nil {
		var taskDoesNotExist *types.TaskDoesNotExist
		var taskTimedOut *types.TaskTimedOut
		var invalidToken *types.InvalidToken
		if err != nil && (errors.As(err, &taskDoesNotExist) || errors.As(err, &taskTimedOut) || errors.As(err, &invalidToken)) {
			return err
		}
		if err == context.Canceled {
			// context was canceled while sending heartbeat
			return nil
		}
		log.ErrorD("heartbeat-error-unknown", logger.M{"error": err.Error()}) // should investigate unknown/unclassified errors
	}
	log.Trace("heartbeat-sent")
	return nil
}
