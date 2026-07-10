package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type videoMetadata struct {
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
}

const sixNine = 16.0 / 9.0
const nineSix = 9.0 / 16
const tolerateError = 0.1

func getVideoAspectRatio(filePath string) (string, error) {
	commandArgsStr := fmt.Sprintf("-v error -print_format json -show_streams %s", filePath)
	commandArgs := strings.Fields(commandArgsStr)
	cmd := exec.Command("ffprobe", commandArgs...)

	var cmdOutput bytes.Buffer
	cmd.Stdout = &cmdOutput
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	videoMeta := videoMetadata{}
	err = json.Unmarshal(cmdOutput.Bytes(), &videoMeta)
	if err != nil {
		return "", err
	}

	ratio := float64(videoMeta.Streams[0].Width) / float64(videoMeta.Streams[0].Height)
	if ratio > sixNine-tolerateError && ratio < sixNine+tolerateError {
		return "16:9", nil
	} else if ratio > nineSix-tolerateError && ratio < nineSix+tolerateError {
		return "9:16", nil
	}
	return "other", nil
}
