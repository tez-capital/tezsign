package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/ulikunitz/xz"
)

// copyFile is a helper function to copy file contents
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

func copyFileToXZ(src, dst string) error {
	if _, err := exec.LookPath("xz"); err == nil {
		destFile, err := os.Create(dst)
		if err != nil {
			return err
		}
		defer destFile.Close()

		var stderr bytes.Buffer
		cmd := exec.Command("xz", "-z", "-9e", "-T1", "-c", src)
		cmd.Stdout = destFile
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("xz command failed: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil
	}

	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	xzWriter, err := xz.NewWriter(destFile)
	if err != nil {
		return err
	}
	defer xzWriter.Close()

	_, err = io.Copy(xzWriter, sourceFile)
	return err
}

type Edit struct {
	Key   string
	Value string
}

// EditTxtFile uses Viper to safely modify key-value pairs in a file.
func EditTxtFile(filePath string, edits []Edit) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	// Ensure the file is closed when the function returns.
	defer file.Close()

	var result strings.Builder
	scanner := bufio.NewScanner(file)

	// **Track which edits have been used.**
	usedEdits := make([]bool, len(edits))

	// Process the file line by line.
	for scanner.Scan() {
		line := scanner.Text()
		originalLine := line
		lineModified := false

		// Check each edit against the current line.
		// We use the index `i` to track which edit is being used.
		for i, edit := range edits {
			prefix := edit.Key + "="
			commentedPrefix := "#" + edit.Key + "="

			// Check if the line starts with the key= or #key=
			if strings.HasPrefix(line, prefix) || strings.HasPrefix(line, commentedPrefix) {
				result.WriteString(edit.Key + "=" + edit.Value + "\n")
				lineModified = true
				// **Mark this edit as used.**
				usedEdits[i] = true
				break
			}
		}

		// If no edit matched the line, keep the original line.
		if !lineModified {
			result.WriteString(originalLine + "\n")
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading file: %w", err)
	}

	// **Append any edits that were not used.**
	for i, used := range usedEdits {
		if !used {
			edit := edits[i]
			result.WriteString(edit.Key + "=" + edit.Value + "\n")
		}
	}

	file.Close() // Close the file before writing back.

	// Write the modified content back to the file.
	err = os.WriteFile(filePath, []byte(result.String()), 0644)
	if err != nil {
		return fmt.Errorf("error writing file: %w", err)
	}

	return nil
}

type mount struct {
	point   string
	options []string
}

func PathFsTab(filePath string, mounts []mount) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	// Ensure the file is closed when the function returns.
	defer file.Close()

	var result strings.Builder
	scanner := bufio.NewScanner(file)

	usedMounts := make([]bool, len(mounts))

	// Process the file line by line.
	for scanner.Scan() {
		line := scanner.Text()
		originalLine := line
		lineModified := false

		for i, edit := range mounts {
			if strings.HasPrefix(line, edit.point) {
				result.WriteString(edit.point + " " + strings.Join(edit.options, " ") + "\n")
				lineModified = true
				usedMounts[i] = true
				break
			}
		}

		if !lineModified {
			result.WriteString(originalLine + "\n")
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading file: %w", err)
	}

	for i, used := range usedMounts {
		if !used {
			edit := mounts[i]
			result.WriteString(edit.point + " " + strings.Join(edit.options, " ") + "\n")
		}
	}

	file.Close() // Close the file before writing back.

	// Write the modified content back to the file.
	err = os.WriteFile(filePath, []byte(result.String()), 0644)
	if err != nil {
		return fmt.Errorf("error writing file: %w", err)
	}

	return nil
}
