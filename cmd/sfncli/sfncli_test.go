package main

import (
	"context"
	"errors"
	"io/ioutil"
	"os"
	"testing"

	"github.com/Clever/sfncli/mocks"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateWorkDirectory(t *testing.T) {
	t.Run("creates directory if not exist", func(t *testing.T) {
		dirname := "/tmp/hello-there"
		defer os.RemoveAll(dirname)
		_, err := os.Stat(dirname)
		assert.True(t, os.IsNotExist(err))

		err = validateWorkDirectory(dirname)
		assert.NoError(t, err)

		_, err = os.Stat(dirname)
		assert.True(t, !os.IsNotExist(err))
	})

	t.Run("fails if not a directory", func(t *testing.T) {
		f, err := ioutil.TempFile("/tmp", "filename")
		defer os.Remove(f.Name())

		err = validateWorkDirectory(f.Name())
		assert.Error(t, err)
	})
}

func TestPollActivityTask(t *testing.T) {
	const arn, worker, token = "arn:activity", "worker", "tok"

	t.Run("no task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		m := mocks.NewMockSFNAPI(ctrl)
		m.EXPECT().GetActivityTask(gomock.Any(), gomock.Any()).Return(&sfn.GetActivityTaskOutput{}, nil)

		out, err := pollActivityTask(context.Background(), m, arn, worker)
		require.NoError(t, err)
		require.Nil(t, out)
	})

	t.Run("task while running is returned", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		m := mocks.NewMockSFNAPI(ctrl)
		m.EXPECT().GetActivityTask(gomock.Any(), gomock.Any()).
			Return(&sfn.GetActivityTaskOutput{TaskToken: aws.String(token), Input: aws.String("{}")}, nil)

		out, err := pollActivityTask(context.Background(), m, arn, worker)
		require.NoError(t, err)
		require.Equal(t, token, *out.TaskToken)
	})

	t.Run("SIGTERM mid-poll does not cancel the poll, task is handed back", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		m := mocks.NewMockSFNAPI(ctrl)
		stopCtx, stop := context.WithCancel(context.Background())

		m.EXPECT().GetActivityTask(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, _ *sfn.GetActivityTaskInput, _ ...func(*sfn.Options)) (*sfn.GetActivityTaskOutput, error) {
				stop() // SIGTERM arrives mid-poll
				require.NoError(t, ctx.Err(), "poll context must survive SIGTERM")
				return &sfn.GetActivityTaskOutput{TaskToken: aws.String(token), Input: aws.String("{}")}, nil
			})
		f := TaskFailureWorkerShuttingDown{}
		m.EXPECT().SendTaskFailure(gomock.Any(), &sfn.SendTaskFailureInput{
			TaskToken: aws.String(token),
			Error:     aws.String(f.ErrorName()),
			Cause:     aws.String(f.ErrorCause()),
		}).Return(&sfn.SendTaskFailureOutput{}, nil)

		out, err := pollActivityTask(stopCtx, m, arn, worker)
		require.NoError(t, err)
		require.Nil(t, out)
	})

	t.Run("hand-back failure is reported", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		m := mocks.NewMockSFNAPI(ctrl)
		stopCtx, stop := context.WithCancel(context.Background())
		stop()
		m.EXPECT().GetActivityTask(gomock.Any(), gomock.Any()).
			Return(&sfn.GetActivityTaskOutput{TaskToken: aws.String(token)}, nil)
		m.EXPECT().SendTaskFailure(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))

		out, err := pollActivityTask(stopCtx, m, arn, worker)
		require.Error(t, err)
		require.Nil(t, out)
	})
}
