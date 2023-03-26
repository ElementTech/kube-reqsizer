package kubegit

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/go-github/v50/github"
	"github.com/jatalocks/kube-reqsizer/types"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func UpdateContainerRequestsInFile(filePath string, containerReqs []types.NewContainerRequests, ghToken, owner, repo string) error {
	// Read the YAML file and unmarshal it into an unstructured object
	file, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %v", err)
	}
	var obj unstructured.Unstructured
	if err := yaml.Unmarshal(file, &obj); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %v", err)
	}

	// Find the containers in the YAML that match the container names in the requests and update their requests
	containers, _, err := unstructured.NestedSlice(obj.Object, "spec", "containers")
	if err != nil {
		return fmt.Errorf("failed to get containers from YAML: %v", err)
	}
	for i, container := range containers {
		name, _, err := unstructured.NestedString(container.(map[string]interface{}), "name")
		if err != nil {
			continue
		}
		for _, req := range containerReqs {
			if name == req.Name {
				reqMap := map[string]interface{}{
					"resources": map[string]interface{}{
						"requests": req.Requests.Requests,
					},
				}
				if err := unstructured.SetNestedMap(containers[i].(map[string]interface{}), reqMap, "resources"); err != nil {
					return fmt.Errorf("failed to set resource requests for container %q: %v", req.Name, err)
				}
			}
		}
	}

	// Marshal the updated YAML back to a string and write it to the file
	newFile, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal updated YAML: %v", err)
	}
	if err := os.WriteFile(filePath, newFile, 0644); err != nil {
		return fmt.Errorf("failed to write updated file: %v", err)
	}

	// Create a pull request for the updated file, if one doesn't already exist
	prTitle := "Update resource requests for containers"
	prBody := fmt.Sprintf("Update resource requests for %d containers in file %q", len(containerReqs), filePath)
	head := fmt.Sprintf("%s:%s", owner, filepath.Base(filePath))
	base := "main"
	client := github.NewClient(&http.Client{})
	ctx := context.Background()
	pullRequests, _, err := client.PullRequests.List(ctx, owner, repo, nil)
	if err != nil {
		return fmt.Errorf("failed to list pull requests: %v", err)
	}
	var existingPullRequest *github.PullRequest
	for _, pr := range pullRequests {
		if pr.Head.GetRef() == head && pr.Base.GetRef() == base {
			existingPullRequest = pr
			break
		}
	}
	if existingPullRequest == nil {
		newPR := &github.NewPullRequest{
			Title: &prTitle,
			Head:  &head,
			Base:  &base,
			Body:  &prBody,
		}
		if _, _, err := client.PullRequests.Create(ctx, owner, repo, newPR); err != nil {
			return fmt.Errorf("failed to create pull request: %v", err)
		}
	}

	return nil
}
