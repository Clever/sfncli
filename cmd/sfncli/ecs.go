package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
)

const magicECSTaskARN = "MAGIC_ECS_TASK_ARN"

func getDockerID() (string, error) {
	file, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		// Example: "2:cpu:/docker/93c562c426414f53582c9830a30bdb54d85642956e18115dd59bc9f435ae5644"
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}

		components := strings.Split(line, ":")
		if len(components) == 3 {
			return strings.TrimRight(path.Base(components[2]), "\n"), nil
		}
	}

	return "", fmt.Errorf("Failed to find Docker ID in /proc/self/group")
}

type ECSAgentMetadata struct {
	Cluster string `json:"Cluster"`
}

type ECSAgentTaskMetadata struct {
	Tasks []struct {
		ARN        string `json:"Arn"`
		Containers []struct {
			DockerID string `json:"DockerId"`
		} `json:"Containers"`
	} `json:"Tasks"`
}

// https://github.com/aws/amazon-ecs-agent/issues/258
// https://github.com/aws/amazon-ecs-agent/pull/709
func ecsAgentTaskMetadata() (ECSAgentTaskMetadata, error) {
	response, err := http.Get("http://172.17.0.1:51678/v1/tasks")
	if err != nil {
		return ECSAgentTaskMetadata{}, err
	}
	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return ECSAgentTaskMetadata{}, err
	}

	metadata := ECSAgentTaskMetadata{}
	err = json.Unmarshal(body, &metadata)
	if err != nil {
		return ECSAgentTaskMetadata{}, err
	}

	return metadata, nil
}

func ecsAgentMetadata() (ECSAgentMetadata, error) {
	response, err := http.Get("http://172.17.0.1:51678/v1/metadata")
	if err != nil {
		return ECSAgentMetadata{}, err
	}
	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return ECSAgentMetadata{}, err
	}

	metadata := ECSAgentMetadata{}
	err = json.Unmarshal(body, &metadata)
	if err != nil {
		return ECSAgentMetadata{}, err
	}

	return metadata, nil
}

func expandECSTaskARN(s string) (string, error) {
	if !strings.Contains(s, magicECSTaskARN) {
		return s, nil
	}

	dockerID, err := getDockerID()
	if err != nil {
		return "", err
	}

	agentMetadata, err := ecsAgentMetadata()
	if err != nil {
		return "", err
	}

	if agentMetadata.Cluster == "" {
		return "", fmt.Errorf("Could not find ECS cluster for docker container '%s'", dockerID)
	}

	agentTaskMetadata, err := ecsAgentTaskMetadata()
	if err != nil {
		return "", err
	}

	taskARN := ""
	for _, task := range agentTaskMetadata.Tasks {
		for _, container := range task.Containers {
			if strings.HasPrefix(container.DockerID, dockerID) {
				taskARN = task.ARN
				break
			}
		}
	}
	if taskARN == "" {
		return "", fmt.Errorf("Could not find ECS task for docker container '%s' on cluster '%s'", dockerID, agentMetadata.Cluster)
	}

	return strings.Replace(s, magicECSTaskARN, taskARN, 1), nil
}
