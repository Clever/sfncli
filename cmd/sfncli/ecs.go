package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"
)

const magicECSTaskARN = "MAGIC_ECS_TASK_ARN"
const magicECSTaskID = "MAGIC_ECS_TASK_ID"

// ecsAgentURIEnvVar is set by the ECS agent (>= v1.67.0) and used for task protection API
const ecsAgentURIEnvVar = "ECS_AGENT_URI"

// taskProtectionExpiryMinutes is the fallback expiry for task protection in case the process
// exits without explicitly removing protection (e.g. crash). 120 minutes covers our longest jobs.
const taskProtectionExpiryMinutes = 120

// setTaskProtection enables or disables ECS scale-in protection for the current task.
// When enabled, ECS will not terminate this task during autoscaling scale-in events.
// No-op if ECS_AGENT_URI is not set (e.g. local development).
func setTaskProtection(protected bool) error {
	agentURI, ok := os.LookupEnv(ecsAgentURIEnvVar)
	if !ok {
		return nil
	}
	body := fmt.Sprintf(`{"ProtectionEnabled":%v,"ExpiresInMinutes":%d}`, protected, taskProtectionExpiryMinutes)
	req, err := http.NewRequest("PUT", agentURI+"/task-protection/v1/state", strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating task protection request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending task protection request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("task protection request returned %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// TODO https://clever.atlassian.net/browse/INFRANG-4174. Update URI env variable
// these are env vars the AWS ECS agent sets for us depending on ECS agent version or Fargate platform version
const ecsContainerMetadataUriEnvVar = "ECS_CONTAINER_METADATA_URI"
const ecsContainerMetadataFileEnvVar = "ECS_CONTAINER_METADATA_FILE"

// ecsContainerMetadata is a subset of fields in the container metadata file. Doc for reference:
// https://docs.aws.amazon.com/AmazonECS/latest/developerguide/container-metadata.html#metadata-file-format
type ecsContainerMetadata struct {
	TaskARN            string
	MetadataFileStatus string
}

// ecsTaskMetadata is a subset of fields in the task metadata JSON response. Doc for reference:
// https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task-metadata-endpoint.html
type ecsTaskMetadata struct {
	TaskARN string
}

// expandECSMagicStrings uses ECS Container Metadata to magically populate the TaskARN or ID required to
// register with AWS Step Functions.
func expandECSMagicStrings(s string) (string, error) {
	if !strings.Contains(s, magicECSTaskARN) && !strings.Contains(s, magicECSTaskID) {
		return s, nil
	}

	arn, err := lookupARN()
	if err != nil {
		return "", err
	}

	arnParts := strings.Split(arn, "/")
	if len(arnParts) == 0 {
		return "", fmt.Errorf("task ARN did not contain '/'. ARN: %s", arn)
	}
	taskID := arnParts[len(arnParts)-1]

	s = strings.Replace(s, magicECSTaskARN, arn, 1)
	return strings.Replace(s, magicECSTaskID, taskID, 1), nil
}

// lookupARN attempts to lookup the ARN of the running task, preferring to use the metadata endpoint, or failing that, checking the metadata file
func lookupARN() (string, error) {
	arn, errURI := arnFromMetadataURI()
	if errURI == nil {
		return arn, nil
	}
	arn, errFile := arnFromMetadataFile()
	if errFile == nil {
		return arn, nil
	}
	return "", errors.New(errURI.Error() + errFile.Error())
}

func arnFromMetadataURI() (string, error) {
	uri, ok := os.LookupEnv(ecsContainerMetadataUriEnvVar)
	if !ok {
		return "", fmt.Errorf("%s not set", ecsContainerMetadataUriEnvVar)
	}
	resp, err := http.Get(uri + "/task")
	if err != nil {
		return "", fmt.Errorf("getting task metadata URI: %v", err)
	}
	var metadata ecsTaskMetadata
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response from metadata endpoint: %v", err)
	}
	if err := json.Unmarshal(body, &metadata); err != nil {
		return "", fmt.Errorf("unmarshaling response from metadata endpoint: %v\nresponse was %s", err, string(body))
	}
	return metadata.TaskARN, nil
}

func arnFromMetadataFile() (string, error) {
	filePath, ok := os.LookupEnv(ecsContainerMetadataFileEnvVar)
	if !ok {
		return "", fmt.Errorf("%s not set", ecsContainerMetadataFileEnvVar)
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

	return metadata.TaskARN, nil
}
