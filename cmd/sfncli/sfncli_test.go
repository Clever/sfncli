package main

import (
	"context"
	"errors"
	"io/ioutil"
	"os"
	"testing"

	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
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

func TestIsCanceledError(t *testing.T) {
	t.Run("bare context.Canceled", func(t *testing.T) {
		assert.True(t, isCanceledError(context.Canceled))
	})

	t.Run("smithy.CanceledError wrapping context.Canceled", func(t *testing.T) {
		err := &smithy.CanceledError{Err: context.Canceled}
		assert.True(t, isCanceledError(err))
	})

	t.Run("smithy.OperationError wrapping a canceled request (production shape)", func(t *testing.T) {
		err := &smithy.OperationError{
			ServiceID:     "SFN",
			OperationName: "GetActivityTask",
			Err:           &smithy.CanceledError{Err: context.Canceled},
		}
		assert.True(t, isCanceledError(err))
	})

	t.Run("unrelated error", func(t *testing.T) {
		assert.False(t, isCanceledError(errors.New("boom")))
	})
}
