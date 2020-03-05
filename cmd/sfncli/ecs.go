package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"
)

const magicECSTaskARN = "MAGIC_ECS_TASK_ARN"

// ecsContainerMetadata is a subset of fields in the container metadata file. Doc for reference:
// https://docs.aws.amazon.com/AmazonECS/latest/developerguide/container-metadata.html#metadata-file-format
type ecsContainerMetadata struct {
	TaskARN            string
	MetadataFileStatus string
}

// expandECSTaskARN uses ECS Container Metadata to magically populate the TaskARN required to
// register with AWS Step Functions. Doc for reference:
// https://docs.aws.amazon.com/AmazonECS/latest/developerguide/container-metadata.html
func expandECSTaskARN(s string) (string, error) {
	if !strings.Contains(s, magicECSTaskARN) {
		return s, nil
	}

	// this is an env var the AWS ECS agent sets for us
	filePath, ok := os.LookupEnv("ECS_CONTAINER_METADATA_FILE")
	if !ok {
		return "", fmt.Errorf("ECS_CONTAINER_METADATA_FILE is not set")
	}

	// wait for the file to exist
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		<-ticker.C
		if _, err := os.Stat(filePath); err == nil {
			break
		}
	}

	// wait until the file data has the TaskARN
	var metadata ecsContainerMetadata
	for {
		b, err := ioutil.ReadFile(filePath)
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(b, &metadata); err != nil {
			return "", err
		}
		if metadata.TaskARN != "" || metadata.MetadataFileStatus == "READY" {
			break
		}
		<-ticker.C
	}

	return strings.Replace(s, magicECSTaskARN, metadata.TaskARN, 1), nil
}
