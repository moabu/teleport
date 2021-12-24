/*
Copyright 2021 Gravitational, Inc.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"

	"github.com/gravitational/trace"
)

func main() {
	input, err := parseInput()
	if err != nil {
		log.Fatal(err)
	}
	for _, baseBranch := range input.backportBranches {
		newBranchName, err := backport(baseBranch, input)
		if err != nil {
			log.Fatal(err)
		}
		err = createPullRequest(baseBranch, newBranchName)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Pull request created for %s.\n", newBranchName)

		err = checkout(input.startingBranch)
		if err != nil {
			log.Fatal(err)
		}
	}
	fmt.Println("Backporting complete.")
}

type Input struct {
	// backportBranches is a list of branches to backport to.
	backportBranches []string

	// from is the name of the branch to pick the commits from.
	fromBranch string

	// mergeBaseCommit is the merge base commit.
	mergeBaseCommit string

	// headCommit is the HEAD of the from branch.
	headCommit string

	// startingBranch is the current branch.
	startingBranch string
}

func parseInput() (Input, error) {
	var to string
	var from string
	flag.StringVar(&to, "to", "", "List of comma-separated branch names to backport to.\n Ex: branch/v6,branch/v7\n")
	flag.StringVar(&from, "from", "", "Branch with changes to backport.")
	flag.Parse()

	if to == "" {
		return Input{}, trace.BadParameter("must supply branches to backport to.")
	}
	if from == "" {
		return Input{}, trace.BadParameter("much supply branch with changes to backport.")
	}
	// Parse branches to backport to.
	backportBranches, err := parseBranches(to)
	if err != nil {
		return Input{}, trace.Wrap(err)
	}

	// To cherry pick all commits from a branch, the merge-base and
	// HEAD of the branch commits are needed.
	mbCommit, err := getMergeBaseCommit(from)
	if err != nil {
		return Input{}, trace.Wrap(err)
	}

	head, err := getHeadFromBranch(from)
	if err != nil {
		return Input{}, trace.Wrap(err)
	}

	// Get the current branch to checkout when 
	// backport is complete. 
	currentBranchName, err := getCurrentBranch()
	if err != nil {
		return Input{}, trace.Wrap(err)
	}

	return Input{
		backportBranches: backportBranches,
		fromBranch:       from,
		mergeBaseCommit:  mbCommit,
		headCommit:       head,
		startingBranch:   currentBranchName,
	}, nil
}

// backport creates a new branch against the passed in branch, cherry-picks
// the changes from the branch and pushes the changes.
func backport(baseBranch string, input Input) (string, error) {
	newBranchName, err := createBranch(input.fromBranch, baseBranch)
	if err != nil {
		return "", trace.Wrap(err)
	}
	fmt.Printf("New branch %s created.\n", newBranchName)

	// Checkout the new branch. This will fail if there are any uncommitted changes.
	// The working tree MUST be clean.
	err = checkout(newBranchName)
	if err != nil {
		defer func() {
			if cleanUpErr := cleanUp(newBranchName, input.startingBranch); cleanUpErr != nil {
				fmt.Printf("Failed to clean up branch. please manually delete %s. Error: %v\n", newBranchName, cleanUpErr)
			}
		}()
		fmt.Println("*** Ensure your working tree is clean. ***")
		return "", trace.Wrap(err)
	}

	err = cherryPick(input.mergeBaseCommit, input.headCommit)
	if err != nil {
		defer func() {
			if cleanUpErr := cleanUp(newBranchName, input.startingBranch); cleanUpErr != nil {
				fmt.Printf("Failed to clean up branch. please manually delete %s. Error: %v\n", newBranchName, cleanUpErr)
			}
		}()
		return "", trace.Wrap(err)
	}
	fmt.Printf("Cherry picked %s-%s to branch %s based off of branch %s.\n",
		input.mergeBaseCommit, input.headCommit, newBranchName, baseBranch)

	// Push new branch to remote.
	err = push(newBranchName)
	if err != nil {
		return "", trace.Wrap(err)
	}
	fmt.Println("Changes pushed successfully.")
	return newBranchName, nil
}

// cleanUp checks out the branchToEndOn and
// deletes the branchToDelete.
func cleanUp(branchToDelete, branchToEndOn string) error {
	err := checkout(branchToEndOn)
	if err != nil {
		return trace.Wrap(err)
	}
	return deleteBranch(branchToDelete)
}

// deleteBranch deletes the specified branch name.
func deleteBranch(branchName string) error {
	_, _, err := run("git", "branch", "-D", branchName)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// getCurrentBranch gets the current working branch.
func getCurrentBranch() (string, error) {
	currentBranchName, _, err := run("git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", trace.Wrap(err)
	}
	// The output of the ran command has a newline.
	currentBranchName = strings.Trim(currentBranchName, "\n")
	if currentBranchName == "" {
		return "", trace.Errorf("failed to get the current branch")
	}
	return currentBranchName, nil
}

// parseBranches parses the input branches to backport to.
func parseBranches(branchesInput string) ([]string, error) {
	var backportBranches []string
	branches := strings.Split(branchesInput, ",")
	for _, branch := range branches {
		if branch == "" {
			return nil, trace.BadParameter("recieved an empty branch name.")
		}
		backportBranches = append(backportBranches, strings.TrimSpace(branch))
	}
	return backportBranches, nil
}

// push pushes changes to the remote repository configured
// in `.git/config` located in the project root.
func push(backportBranchName string) error {
	_, _, err := run("git", "push", "--set-upstream", "origin", backportBranchName)
	if err != nil {
		return trace.BadParameter("failed to push branch %s: %v", backportBranchName, err)
	}
	return nil
}

// createPullRequest creates a pull request with the credentials stored
// in ~/.config/gh/hosts.yaml.
func createPullRequest(baseBranch, headBranch string) error {
	_, _, err := run("gh", "pr", "create", "--fill", "--label", "backport", "--base", baseBranch, "--head", headBranch)
	if err != nil {
		return trace.BadParameter("failed to create a pull request for %s: %v.\n Open up a pull request on github.com.", headBranch, err)
	}
	return nil
}

// checkout checks out the specified branch.
func checkout(branch string) error {
	stdout, stderr, err := run("git", "checkout", branch)
	if err != nil {
		return trace.BadParameter("failed to checkout a branch: %s: %s: %v", stdout, stderr, err)
	}
	return nil
}

// getHeadFromBranch gets the head commit from the given branch.
func getHeadFromBranch(branch string) (string, error) {
	latestCommit, _, err := run("git", "log", "-n", "1", "--pretty=format:\"%H\"", branch)
	if err != nil {
		return "", trace.Wrap(err)
	}
	latestCommit = parseCommit(latestCommit)
	if latestCommit == "" {
		return "", trace.Errorf("failed to get the HEAD for %s: %v ", branch, err)
	}
	return latestCommit, nil
}

var commmitPattern = regexp.MustCompile(`\b([a-f0-9]{40})\b`)

// parseCommit parses an input for a commit hash.
func parseCommit(input string) string {
	input = commmitPattern.FindString(input)
	return input
}

// getMergeBaseCommit gets the commit where the branch merged off
// of master (i.e. most recent common ancestor).
func getMergeBaseCommit(branchToCherryPickFrom string) (string, error) {
	mergeBaseCommit, _, err := run("git", "merge-base", "master", branchToCherryPickFrom)
	if err != nil {
		return "", trace.Wrap(err)
	}
	mergeBaseCommit = parseCommit(mergeBaseCommit)
	if mergeBaseCommit == "" {
		return "", trace.Errorf("failed to get the merge base commit of %s", branchToCherryPickFrom)
	}
	return mergeBaseCommit, nil
}

// createBranch creates a new branch based off of the base branch.
func createBranch(fromBranchName, baseBranchName string) (string, error) {
	newBranchName := fmt.Sprintf("auto-backport/%s/%s", baseBranchName, fromBranchName)
	stdout, stderr, err := run("git", "branch", newBranchName, baseBranchName)
	if err != nil {
		return "", trace.BadParameter("failed to create a new branch %s: %s: %v.", stdout, stderr, err)
	}
	return newBranchName, nil
}

// cherryPick cherry picks a range of commits. The first
// commit is not included.
func cherryPick(mergeBaseCommit, headCommitOfBranchWithChanges string) error {
	if mergeBaseCommit == headCommitOfBranchWithChanges {
		return trace.BadParameter("there are no changes to backport.")
	}
	commitRange := fmt.Sprintf("%s..%s", mergeBaseCommit, headCommitOfBranchWithChanges)
	_, _, err := run("git", "cherry-pick", commitRange)
	if err != nil {
		return trace.BadParameter("failed to cherry-pick %s-%s: %v", mergeBaseCommit, headCommitOfBranchWithChanges, err)
	}
	return nil
}

// run executes command on disk.
func run(ex string, command ...string) (string, string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	path, err := exec.LookPath(ex)
	if err != nil {
		return "", "", err
	}
	cmd := exec.Command(path, command...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	return stdout.String(), stderr.String(), err
}
