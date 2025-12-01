package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/filesystem"
)

func ensureImageFlavour(fs filesystem.FileSystem, fallback string, logger *slog.Logger) (string, error) {
	flavour, err := readImageFlavour(fs)
	if err != nil {
		return "", err
	}
	if flavour != "" {
		return flavour, nil
	}
	if fallback == "" {
		return "", errors.New("unable to determine image flavour")
	}

	if tmp, err := fs.OpenFile("/.image-flavour", os.O_WRONLY|os.O_CREATE|os.O_TRUNC); err == nil {
		if _, err := tmp.Write([]byte(fallback)); err != nil {
			logger.Debug("Failed to write /.image-flavour; continuing", "error", err)
		}
		tmp.Close()
		_ = fs.Chmod("/.image-flavour", 0444)
	} else {
		logger.Debug("Failed to persist /.image-flavour; continuing", "error", err)
	}
	return fallback, nil
}

func performAppBinaryUpdate(binaryPath, destination string, logger *slog.Logger) error {
	logger.Info("Starting TezSign app-only update", "source", binaryPath, "destination", destination)

	if err := ensureMountAvailable(); err != nil {
		return err
	}

	dstImg, _, _, destinationAppPartition, err := loadImage(destination, diskfs.ReadWriteExclusive)
	if err != nil {
		return fmt.Errorf("failed to load destination image: %w", err)
	}
	defer dstImg.Close()

	if ok, err := checkTezsignMarker(dstImg); err != nil {
		return fmt.Errorf("marker check failed: %w", err)
	} else if !ok {
		return errors.New("destination does not match TezSign layout; aborting")
	}

	fs, err := filesystemForPartition(dstImg, destinationAppPartition)
	if err != nil {
		return fmt.Errorf("failed to open app filesystem: %w", err)
	}

	table, err := dstImg.GetPartitionTable()
	if err != nil {
		return fmt.Errorf("failed to read partition table: %w", err)
	}

	currentFlavour, _ := readImageFlavour(fs)
	fallback := flavourFromTable(table)
	if currentFlavour != "" {
		fallback = currentFlavour
	}
	flavour, err := ensureImageFlavour(fs, fallback, logger)
	if err != nil {
		return fmt.Errorf("failed to ensure image flavour: %w", err)
	}
	logger.Info("Using image flavour", "flavour", flavour)

	in, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("failed to open gadget binary: %w", err)
	}
	defer in.Close()

	// Always use mount-based write; direct go-diskfs writes are unreliable on RO-marked filesystems.
	if err := writeAppViaMount(binaryPath, flavour, logger); err != nil {
		return fmt.Errorf("failed to write gadget binary via mount: %w", err)
	}

	return nil
}

func writeAppViaMount(binaryPath, flavour string, logger *slog.Logger) error {
	appDev, err := filepath.EvalSymlinks("/dev/disk/by-label/app")
	if err != nil {
		return fmt.Errorf("failed to resolve /dev/disk/by-label/app: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "tezsign_app_mount_")
	if err != nil {
		return fmt.Errorf("failed to create temp mount dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	mountCmd := exec.Command("mount", "-o", "rw", appDev, tmpDir)
	if out, err := mountCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to mount app partition (%s): %v: %s", appDev, err, string(out))
	}
	defer exec.Command("umount", tmpDir).Run()

	dstPath := filepath.Join(tmpDir, "tezsign")
	src, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("failed to open gadget binary: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing: %w", dstPath, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("failed to write gadget binary via mount: %w", err)
	}
	dst.Close()
	_ = os.Chmod(dstPath, 0755)

	flavourPath := filepath.Join(tmpDir, ".image-flavour")
	if _, err := os.Stat(flavourPath); os.IsNotExist(err) && flavour != "" {
		if err := os.WriteFile(flavourPath, []byte(flavour), 0444); err != nil {
			logger.Debug("Failed to persist .image-flavour via mount; continuing", "error", err)
		}
	}

	if out, err := exec.Command("sync").CombinedOutput(); err != nil {
		logger.Debug("sync failed after mount write", "error", err, "output", string(out))
	}

	return nil
}

func ensureMountAvailable() error {
	if _, err := exec.LookPath("mount"); err != nil {
		return fmt.Errorf("mount binary not found: %w", err)
	}
	if _, err := exec.LookPath("umount"); err != nil {
		return fmt.Errorf("umount binary not found: %w", err)
	}
	if _, err := os.Stat("/dev/disk/by-label/app"); err != nil {
		return fmt.Errorf("app partition label not found: %w", err)
	}
	return nil
}
