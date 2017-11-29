package main

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Clever/sfncli/gen-go/mocksfn"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/sfn"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
)

const (
	mockTaskToken  = "taskToken"
	emptyTaskInput = "{}"
	testScriptsDir = "./test_scripts"
)

type workdirMatcher struct {
	taskToken      string
	expectedPrefix string
	foundWorkdir   string
}

func (w workdirMatcher) String() string {
	return "test the prefix of the work_dir value"
}

func (w *workdirMatcher) Matches(x interface{}) bool {
	input, ok := x.(*sfn.SendTaskSuccessInput)
	if !ok {
		return false
	}

	// check token
	if *input.TaskToken != w.taskToken {
		return false
	}

	workdirBlob := struct {
		Dir string `json:"work_dir"`
	}{}
	if err := json.Unmarshal([]byte(*input.Output), &workdirBlob); err != nil {
		return false
	}

	w.foundWorkdir = workdirBlob.Dir
	return strings.HasPrefix(workdirBlob.Dir, w.expectedPrefix)
}

func TestTaskFailureTaskInputNotJSON(t *testing.T) {
	t.Parallel()
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	cmd := "echo"
	cmdArgs := []string{}
	taskInput := "notjson"
	expectedError := TaskFailureTaskInputNotJSON{input: "notjson"}

	controller := gomock.NewController(t)
	defer controller.Finish()
	mockSFN := mocksfn.NewMockSFNAPI(controller)
	mockSFN.EXPECT().SendTaskFailure(&sfn.SendTaskFailureInput{
		Cause:     aws.String(expectedError.ErrorCause()),
		Error:     aws.String(expectedError.ErrorName()),
		TaskToken: aws.String(mockTaskToken),
	})
	taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
	err := taskRunner.Process(testCtx, cmdArgs, taskInput)
	require.Equal(t, err, expectedError)

}

func TestTaskOutputEmptyStringAsJSON(t *testing.T) {
	t.Parallel()
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	cmd := "stdout_empty_output.sh"
	cmdArgs := []string{}
	taskInput := "{}"

	controller := gomock.NewController(t)
	defer controller.Finish()
	mockSFN := mocksfn.NewMockSFNAPI(controller)
	mockSFN.EXPECT().SendTaskSuccessWithContext(gomock.Any(), &sfn.SendTaskSuccessInput{
		TaskToken: aws.String(mockTaskToken),
		Output:    aws.String("{}"),
	})
	taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
	err := taskRunner.Process(testCtx, cmdArgs, taskInput)
	require.NoError(t, err)

}

func TestTaskFailureCommandNotFound(t *testing.T) {
	t.Parallel()
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	cmd := "doesntexist.sh"
	cmdArgs := []string{}
	expectedError := TaskFailureCommandNotFound{path: path.Join(testScriptsDir, cmd)}

	controller := gomock.NewController(t)
	defer controller.Finish()
	mockSFN := mocksfn.NewMockSFNAPI(controller)
	mockSFN.EXPECT().SendTaskFailure(&sfn.SendTaskFailureInput{
		Cause:     aws.String(expectedError.ErrorCause()),
		Error:     aws.String(expectedError.ErrorName()),
		TaskToken: aws.String(mockTaskToken),
	})
	taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
	err := taskRunner.Process(testCtx, cmdArgs, emptyTaskInput)
	require.Equal(t, err, expectedError)
}

func TestTaskFailureCommandKilled(t *testing.T) {
	t.Parallel()
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	cmd := "log_to_stderr_and_wait.sh"
	cmdArgs := []string{"log this to stderr"}
	expectedError := TaskFailureCommandKilled{stderr: cmdArgs[0]}

	controller := gomock.NewController(t)
	defer controller.Finish()
	mockSFN := mocksfn.NewMockSFNAPI(controller)
	mockSFN.EXPECT().SendTaskFailure(&sfn.SendTaskFailureInput{
		Cause:     aws.String(expectedError.ErrorCause()),
		Error:     aws.String(expectedError.ErrorName()),
		TaskToken: aws.String(mockTaskToken),
	})
	taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
	go func() {
		time.Sleep(2 * time.Second)
		taskRunner.execCmd.Process.Signal(syscall.SIGKILL)
	}()
	err := taskRunner.Process(testCtx, cmdArgs, emptyTaskInput)
	require.Equal(t, err, expectedError)
}

func TestTaskFailureCommandExitedNonzero(t *testing.T) {
	t.Parallel()
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	cmd := "stderr_stdout_exitcode.sh"
	cmdArgs := []string{"stderr", `{"stdout":"mustbejson"}`, "10"}
	expectedError := TaskFailureCommandExitedNonzero{stderr: "stderr"}

	controller := gomock.NewController(t)
	defer controller.Finish()
	mockSFN := mocksfn.NewMockSFNAPI(controller)
	mockSFN.EXPECT().SendTaskFailure(&sfn.SendTaskFailureInput{
		Cause:     aws.String(expectedError.ErrorCause()),
		Error:     aws.String(expectedError.ErrorName()),
		TaskToken: aws.String(mockTaskToken),
	})
	taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
	err := taskRunner.Process(testCtx, cmdArgs, emptyTaskInput)
	require.Equal(t, err, expectedError)
}

func TestTaskFailureCustomErrorName(t *testing.T) {
	t.Parallel()
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	cmd := "stderr_stdout_exitcode.sh"
	cmdArgs := []string{"stderr", `{"error": "custom.error_name", "cause": "bar"}`, "10"}
	expectedError := TaskFailureCustom{Err: "custom.error_name", Cause: "bar"}

	controller := gomock.NewController(t)
	defer controller.Finish()
	mockSFN := mocksfn.NewMockSFNAPI(controller)
	mockSFN.EXPECT().SendTaskFailure(&sfn.SendTaskFailureInput{
		Cause:     aws.String(expectedError.ErrorCause()),
		Error:     aws.String(expectedError.ErrorName()),
		TaskToken: aws.String(mockTaskToken),
	})
	taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
	err := taskRunner.Process(testCtx, cmdArgs, emptyTaskInput)
	require.Equal(t, err, expectedError)
}

func TestTaskFailureTaskOutputNotJSON(t *testing.T) {
	t.Parallel()
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	cmd := "stderr_stdout_exitcode.sh"
	cmdArgs := []string{"stderr", `stdout not JSON!`, "0"}
	expectedError := TaskFailureTaskOutputNotJSON{output: "stdout not JSON!"}

	controller := gomock.NewController(t)
	defer controller.Finish()
	mockSFN := mocksfn.NewMockSFNAPI(controller)
	mockSFN.EXPECT().SendTaskFailure(&sfn.SendTaskFailureInput{
		Cause:     aws.String(expectedError.ErrorCause()),
		Error:     aws.String(expectedError.ErrorName()),
		TaskToken: aws.String(mockTaskToken),
	})
	taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
	err := taskRunner.Process(testCtx, cmdArgs, emptyTaskInput)
	require.Equal(t, err, expectedError)
}

func TestTaskFailureCommandTerminated(t *testing.T) {
	t.Run("command handles sigterm, exits nonzero", func(t *testing.T) {
		testCtx, testCtxCancel := context.WithCancel(context.Background())
		defer testCtxCancel()
		cmd := "stderr_stdout_exitcode_onsigterm.sh"
		cmdArgs := []string{"stderr", "", "1"}
		expectedError := TaskFailureCommandTerminated{stderr: "stderr"}

		controller := gomock.NewController(t)
		defer controller.Finish()
		mockSFN := mocksfn.NewMockSFNAPI(controller)
		mockSFN.EXPECT().SendTaskFailure(&sfn.SendTaskFailureInput{
			Cause:     aws.String(expectedError.ErrorCause()),
			Error:     aws.String(expectedError.ErrorName()),
			TaskToken: aws.String(mockTaskToken),
		})
		taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
		go func() {
			time.Sleep(1 * time.Second)
			process, _ := os.FindProcess(os.Getpid())
			process.Signal(syscall.SIGTERM)
		}()
		err := taskRunner.Process(testCtx, cmdArgs, emptyTaskInput)
		require.Equal(t, err, expectedError)
	})

	t.Run("command handles sigterm, exits nonzero with custom error code", func(t *testing.T) {
		testCtx, testCtxCancel := context.WithCancel(context.Background())
		defer testCtxCancel()
		cmd := "stderr_stdout_exitcode_onsigterm.sh"
		cmdArgs := []string{"stderr", `{"error": "custom.error_name", "cause": "foo"}`, "1"}
		expectedError := TaskFailureCustom{Err: "custom.error_name", Cause: "foo"}

		controller := gomock.NewController(t)
		defer controller.Finish()
		mockSFN := mocksfn.NewMockSFNAPI(controller)
		mockSFN.EXPECT().SendTaskFailure(&sfn.SendTaskFailureInput{
			Cause:     aws.String(expectedError.ErrorCause()),
			Error:     aws.String(expectedError.ErrorName()),
			TaskToken: aws.String(mockTaskToken),
		})
		taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
		go func() {
			time.Sleep(1 * time.Second)
			process, _ := os.FindProcess(os.Getpid())
			process.Signal(syscall.SIGTERM)
		}()
		err := taskRunner.Process(testCtx, cmdArgs, emptyTaskInput)
		require.Equal(t, err, expectedError)
	})

	t.Run("command does not handle sigterm", func(t *testing.T) {
		testCtx, testCtxCancel := context.WithCancel(context.Background())
		defer testCtxCancel()
		cmd := "stderr_stdout_loopforever.sh"
		cmdArgs := []string{"stderr", ""}
		expectedError := TaskFailureCommandTerminated{stderr: "stderr"}

		controller := gomock.NewController(t)
		defer controller.Finish()
		mockSFN := mocksfn.NewMockSFNAPI(controller)
		mockSFN.EXPECT().SendTaskFailure(&sfn.SendTaskFailureInput{
			Cause:     aws.String(expectedError.ErrorCause()),
			Error:     aws.String(expectedError.ErrorName()),
			TaskToken: aws.String(mockTaskToken),
		})
		taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
		// lower the grace period so this test doesn't take forever
		taskRunner.sigtermGracePeriod = 5 * time.Second
		go func() {
			time.Sleep(1 * time.Second)
			process, _ := os.FindProcess(os.Getpid())
			process.Signal(syscall.SIGTERM)
		}()
		err := taskRunner.Process(testCtx, cmdArgs, emptyTaskInput)
		require.Equal(t, err, expectedError)
	})
}

func TestTaskSuccessSignalForwarded(t *testing.T) {
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	cmd := "signal_echo.sh"
	cmdArgs := []string{}

	controller := gomock.NewController(t)
	mockSFN := mocksfn.NewMockSFNAPI(controller)
	mockSFN.EXPECT().SendTaskSuccessWithContext(gomock.Any(), &sfn.SendTaskSuccessInput{
		Output:    aws.String(`{"signal":"1"}`),
		TaskToken: aws.String(mockTaskToken),
	})
	defer controller.Finish()
	taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
	go func() {
		time.Sleep(1 * time.Second)
		process, _ := os.FindProcess(os.Getpid())
		process.Signal(syscall.SIGHUP)
	}()
	require.Nil(t, taskRunner.Process(testCtx, cmdArgs, emptyTaskInput))
}

func TestTaskSuccessOutputIsLastLineOfStdout(t *testing.T) {
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	cmd := "stdout_parsing.sh"
	cmdArgs := []string{}

	controller := gomock.NewController(t)
	mockSFN := mocksfn.NewMockSFNAPI(controller)
	mockSFN.EXPECT().SendTaskSuccessWithContext(gomock.Any(), &sfn.SendTaskSuccessInput{
		Output:    aws.String(`{"task":"output"}`),
		TaskToken: aws.String(mockTaskToken),
	})
	defer controller.Finish()
	taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
	require.Nil(t, taskRunner.Process(testCtx, cmdArgs, emptyTaskInput))
}

func TestTaskWorkDirectorySetup(t *testing.T) {
	t.Parallel()
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	cmd := "echo_workdir.sh"
	cmdArgs := []string{}
	taskInput := "{}"

	controller := gomock.NewController(t)
	defer controller.Finish()
	mockSFN := mocksfn.NewMockSFNAPI(controller)
	mockSFN.EXPECT().SendTaskSuccessWithContext(gomock.Any(), &workdirMatcher{
		taskToken:      mockTaskToken,
		expectedPrefix: "/tmp",
	}) // returns the result of WORK_DIR
	taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "/tmp")
	err := taskRunner.Process(testCtx, cmdArgs, taskInput)
	require.NoError(t, err)
}

func TestTaskWorkDirectoryUnsetByDefault(t *testing.T) {
	t.Parallel()
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	cmd := "echo_workdir.sh"
	cmdArgs := []string{}
	taskInput := "{}" // output a env var using the key

	controller := gomock.NewController(t)
	defer controller.Finish()
	mockSFN := mocksfn.NewMockSFNAPI(controller)
	mockSFN.EXPECT().SendTaskSuccessWithContext(gomock.Any(), &sfn.SendTaskSuccessInput{
		TaskToken: aws.String(mockTaskToken),
		Output:    aws.String("{\"work_dir\":\"\"}"), // returns the result of WORK_DIR
	})
	taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "")
	err := taskRunner.Process(testCtx, cmdArgs, taskInput)
	require.NoError(t, err)
}

func TestTaskWorkDirectoryCleaned(t *testing.T) {
	t.Parallel()
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	cmd := "create_file.sh"
	cmdArgs := []string{}
	taskInput := "{}"

	controller := gomock.NewController(t)
	defer controller.Finish()
	mockSFN := mocksfn.NewMockSFNAPI(controller)
	dirMatcher := workdirMatcher{
		taskToken:      mockTaskToken,
		expectedPrefix: "/tmp/test",
	}
	mockSFN.EXPECT().SendTaskSuccessWithContext(gomock.Any(), &dirMatcher) // returns the result of WORK_DIR

	os.MkdirAll("/tmp/test", os.ModeDir|0777) // base path is created by cmd/sfncli/sfncli.go
	defer os.RemoveAll("/tmp/test")
	taskRunner := NewTaskRunner(path.Join(testScriptsDir, cmd), mockSFN, mockTaskToken, "/tmp/test")
	err := taskRunner.Process(testCtx, cmdArgs, taskInput)
	require.NoError(t, err)
	if _, err := os.Stat(dirMatcher.foundWorkdir); os.IsExist(err) {
		require.Fail(t, "directory /tmp/test not deleted")
	}
}
