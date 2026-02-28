package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
)

func isTTY(f *os.File) bool {
	return term.IsTerminal(f.Fd())
}

func resolveImageID(sourcePath string, destPath string, logger *slog.Logger) string {
	imageID := strings.TrimSpace(os.Getenv("IMAGE_ID"))
	if imageID != "" {
		return imageID
	}

	// Fallback for manual runs without IMAGE_ID: infer from filenames when possible.
	candidates := []string{
		strings.ToLower(filepath.Base(sourcePath)),
		strings.ToLower(filepath.Base(destPath)),
	}
	for _, candidate := range candidates {
		switch {
		case strings.Contains(candidate, "raspberry_pi"):
			logger.Warn("IMAGE_ID is not set; inferred from filename", slog.String("image_id", "raspberry_pi"))
			return "raspberry_pi"
		case strings.Contains(candidate, "radxa_zero3") || strings.Contains(candidate, "radxa-zero3"):
			logger.Warn("IMAGE_ID is not set; inferred from filename", slog.String("image_id", "radxa_zero3"))
			return "radxa_zero3"
		}
	}

	return ""
}

func main() {
	// 1. Check for command-line arguments
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run your_program.go <source.img> <destination.img>")
		os.Exit(1)
	}
	sourcePath := os.Args[1]
	destPath := os.Args[2]
	flavour := StandardImage

	if len(os.Args) >= 4 {
		flavour = imageFlavour(os.Args[3])
		switch flavour {
		case StandardImage, DevImage:
			// valid flavour
		default:
			fmt.Println("Invalid image flavour. Valid options are: prod, dev")
			os.Exit(1)
		}
	}

	skipWait := false
	if len(os.Args) == 5 {
		skipWait = os.Args[4] == "--skip-wait"
	}

	fmt.Println()
	fmt.Println()
	fmt.Println("==================== CREATING TEZSIGN IMAGE ====================")
	fmt.Println("Source Image:", sourcePath)
	fmt.Println("Destination Image:", destPath)
	fmt.Println("Image Flavour: -----> ", flavour, "<-----")
	fmt.Println("===============================================================")
	fmt.Println()
	fmt.Println()

	if isTTY(os.Stdout) && !skipWait {
		fmt.Println("Starting in 10 seconds")
		for i := 0; i < 10; i++ {
			fmt.Print(".")
			time.Sleep(1 * time.Second)
		}
		fmt.Println()
	}

	logger := slog.Default()
	imageID := resolveImageID(sourcePath, destPath, logger)

	// Keep temporary build artifacts alongside the destination image directory.
	workDir = filepath.Join(filepath.Dir(destPath), ".tezsign_image_builder")
	tmpImage = filepath.Join(workDir, "image.img")

	logger.Info("Creating working directory", slog.String("path", workDir))
	err := os.MkdirAll(workDir, 0755)
	if err != nil {
		logger.Error("Failed to create working directory", slog.Any("error", err))
		os.Exit(1)
	}

	// 2. Copy the source image to the destination
	logger.Info("Copying image file", slog.String("source", sourcePath), slog.String("destination", tmpImage))
	err = copyFile(sourcePath, tmpImage)
	if err != nil {
		logger.Error("Failed to copy image file", slog.Any("error", err))
		os.Exit(1)
	}

	if err = PartitionImage(tmpImage, flavour, imageID, logger); err != nil {
		logger.Error("Failed to partition image", slog.Any("error", err))
		os.Exit(1)
	}

	if err = ConfigureImage(workDir, tmpImage, flavour, logger); err != nil {
		logger.Error("Failed to configure image", slog.Any("error", err))
		os.Exit(1)
	}

	// 3. Move the modified image to the final destination
	_ = destPath
	// logger.Info("Moving modified image to destination", slog.String("source", tmpImage), slog.String("destination", destPath))

	logger.Info("Copying final image to destination")
	err = copyFileToXZ(tmpImage, destPath)
	defer os.Remove(tmpImage)
	if err != nil {
		logger.Error("Failed to copy final image to destination", slog.Any("error", err))
		os.Exit(1)
	}
	logger.Info("✅ Successfully created the customized image.", slog.String("path", destPath))
}
