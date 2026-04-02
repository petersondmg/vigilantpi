package main

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

var FilesToConvert = make(chan string, 100)

func StartConverter() {
	go func() {
		for tsFile := range FilesToConvert {
			convert(tsFile)
		}
	}()
}

func convert(tsFile string) {
	if _, err := os.Stat(tsFile); os.IsNotExist(err) {
		return
	}

	if !strings.HasSuffix(tsFile, ".ts") {
		return
	}

	// Filename format: rec_YYYY_MM_DD-HH_MM_SS_camera.ext.ts
	fileName := filepath.Base(tsFile)
	parts := strings.SplitN(fileName, "-", 2)
	if len(parts) != 2 {
		return
	}

	dayDir := parts[0]       // rec_YYYY_MM_DD
	rest := parts[1]         // HH_MM_SS_camera.ext.ts
	
	// Final file name is rest minus ".ts"
	finalFileName := strings.TrimSuffix(rest, ".ts")
	targetExt := filepath.Ext(finalFileName)

	// Create final directory
	finalDir := path.Join(videosDir, dayDir)
	if err := os.MkdirAll(finalDir, 0774); err != nil {
		logger.Printf("error creating final directory %s: %s", finalDir, err)
		return
	}

	finalFilePath := path.Join(finalDir, finalFileName)

	logger.Printf("converting %s to %s", tsFile, finalFilePath)

	args := []string{"-y", "-i", tsFile, "-c", "copy"}
	if strings.ToLower(targetExt) == ".mp4" {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, finalFilePath)

	cmd := exec.Command(ffmpeg, args...)
	
	if out, err := cmd.CombinedOutput(); err != nil {
		logger.Printf("error converting %s: %s\nOutput: %s", tsFile, err, string(out))
		return
	}

	logger.Printf("conversion finished: %s", finalFilePath)

	if err := os.Remove(tsFile); err != nil {
		logger.Printf("error removing original ts file %s: %s", tsFile, err)
	}
}

func ScanExistingFiles() {
	go func() {
		tmpDir := path.Join(videosDir, ".tmp")
		if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
			return
		}

		err := filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(path, ".ts") {
				logger.Printf("found orphaned file: %s", path)
				FilesToConvert <- path
			}
			return nil
		})
		if err != nil {
			logger.Printf("error scanning for existing .ts files in .tmp: %s", err)
		}
	}()
}
