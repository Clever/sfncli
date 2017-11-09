package main

import (
	"io/ioutil"
	"os"
	"testing"

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
