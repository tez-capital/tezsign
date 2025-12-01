package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/partition"
	"github.com/diskfs/go-diskfs/partition/gpt"
	"github.com/diskfs/go-diskfs/partition/mbr"
	"github.com/diskfs/go-diskfs/partition/part"
	"github.com/ulikunitz/xz"
)

var validFlavours = map[string]bool{
	"raspberry_pi":     true,
	"raspberry_pi.dev": true,
	"radxa_zero3":      true,
	"radxa_zero3.dev":  true,
}

func maybeDecompressSource(path string, logger *slog.Logger) (string, func(), error) {
	if !strings.HasSuffix(path, ".xz") {
		return path, func() {}, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("failed to open compressed source %s: %w", path, err)
	}
	stat, _ := f.Stat()
	totalBytes := stat.Size()

	cr := &countingReader{r: f}
	r, err := xz.NewReader(cr)
	if err != nil {
		f.Close()
		return "", nil, fmt.Errorf("failed to create xz reader: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "tezsign_img_*.img")
	if err != nil {
		f.Close()
		return "", nil, fmt.Errorf("failed to create temp file for decompression: %w", err)
	}

	logger.Info("Decompressing source image", "source", path, "destination", tmpFile.Name())

	cancel := func() {
		f.Close()
		tmpFile.Close()
	}

	title := fmt.Sprintf("Decompress %s → %s", filepath.Base(path), filepath.Base(tmpFile.Name()))
	p := tea.NewProgram(newProgressModel(title, totalBytes, cr, cancel))

	go func() {
		_, copyErr := io.Copy(tmpFile, r)
		tmpFile.Close()
		f.Close()
		p.Send(finishMsg{err: copyErr})
	}()

	model, progErr := p.Run()
	if progErr != nil {
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("failed to render decompress progress: %w", progErr)
	}

	res, ok := model.(progressModel)
	if !ok {
		os.Remove(tmpFile.Name())
		return "", nil, errors.New("unexpected model type after decompression")
	}

	if res.err != nil {
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("failed to decompress source image: %w", res.err)
	}

	cleanup := func() {
		os.Remove(tmpFile.Name())
	}

	return tmpFile.Name(), cleanup, nil
}

func copyPartitionData(srcDisk *disk.Disk, srcPartition part.Partition, dstDisk *disk.Disk, dstPartition part.Partition, description string, logger *slog.Logger) error {
	pr, pw := io.Pipe()
	writableDst, err := dstDisk.Backend.Writable()
	if err != nil {
		return errors.New("failed to get writable backend for destination disk")
	}

	totalBytes := srcPartition.GetSize()
	counter := &countingWriter{w: pw}
	progress := tea.NewProgram(newProgressModel(fmt.Sprintf("Copying %s", description), totalBytes, counter, nil))

	errCh := make(chan error, 1)

	go func() {
		var wg sync.WaitGroup
		var readErr, writeErr error
		var readBytes int64

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer pw.Close()

			readBytes, readErr = srcPartition.ReadContents(srcDisk.Backend, counter)
			if readErr != nil {
				logger.Error("Failed to read contents from source partition", "error", readErr)
			}
		}()

		writtenBytes, writeErr := dstPartition.WriteContents(writableDst, pr)
		if writeErr != nil {
			logger.Error("Failed to write contents to destination partition", "error", writeErr)
		}
		pr.Close()
		wg.Wait()

		var copyErr error
		if readErr != nil {
			copyErr = errors.New("error occurred while reading from source partition: " + readErr.Error())
		} else if writeErr != nil {
			copyErr = errors.New("error occurred while writing to destination partition: " + writeErr.Error())
		} else if uint64(readBytes) != writtenBytes {
			copyErr = errors.New("mismatch in bytes read and written")
		}

		progress.Send(finishMsg{err: copyErr})
		errCh <- copyErr
	}()

	if _, progErr := progress.Run(); progErr != nil {
		return fmt.Errorf("failed to render copy progress: %w", progErr)
	}

	if copyErr := <-errCh; copyErr != nil {
		return copyErr
	}

	return nil
}

func performUpdate(source, destination string, kind UpdateKind, logger *slog.Logger) error {
	logger.Info("Starting TezSign updater", "source", source, "destination", destination, "kind", string(kind))

	sourcePath, cleanup, err := maybeDecompressSource(source, logger)
	if err != nil {
		return err
	}
	defer cleanup()

	dstImg, destinationBootPartition, destinationRootfsPartition, destinationAppPartition, err := loadImage(destination, diskfs.ReadWriteExclusive)
	if err != nil {
		return fmt.Errorf("failed to load destination image: %w", err)
	}
	defer dstImg.Close()

	if ok, err := checkTezsignMarker(dstImg); err != nil {
		logger.Debug("Skipping marker check", "error", err)
	} else if !ok {
		logger.Debug("Destination missing /tezsign marker; proceeding and will overwrite app partition")
	}

	switch kind {
	case UpdateKindFull:
		sourceImg, sourceBootPartition, sourceRootfsPartition, sourceAppPartition, err := loadImage(sourcePath, diskfs.ReadOnly)
		if err != nil {
			return fmt.Errorf("failed to load source image: %w", err)
		}
		defer sourceImg.Close()

		if (sourceBootPartition == nil || destinationBootPartition == nil) && (sourceBootPartition != destinationBootPartition) {
			return errors.New("boot partition missing in source image or destination device, cannot proceed with full update")
		}
		if sourceBootPartition != nil && sourceBootPartition.GetSize() != destinationBootPartition.GetSize() {
			return errors.New("boot partition size mismatch between source image and destination device, cannot proceed with update")
		}

		if sourceRootfsPartition.GetSize() != destinationRootfsPartition.GetSize() {
			return errors.New("rootfs partition size mismatch between source image and destination device, cannot proceed with update")
		}

		if sourceAppPartition.GetSize() != destinationAppPartition.GetSize() {
			return errors.New("app partition size mismatch between source image and destination device, cannot proceed with update")
		}

		if sourceBootPartition != nil {
			logger.Info("Updating boot partition...")
			if err = copyPartitionData(sourceImg, sourceBootPartition, dstImg, destinationBootPartition, "boot partition", logger); err != nil {
				return fmt.Errorf("failed to update boot partition: %w", err)
			}
		}

		logger.Info("Updating rootfs partition...")
		if err = copyPartitionData(sourceImg, sourceRootfsPartition, dstImg, destinationRootfsPartition, "rootfs partition", logger); err != nil {
			return fmt.Errorf("failed to update rootfs partition: %w", err)
		}

		logger.Info("Updating app partition...")
		if err = copyPartitionData(sourceImg, sourceAppPartition, dstImg, destinationAppPartition, "app partition", logger); err != nil {
			return fmt.Errorf("failed to update app partition: %w", err)
		}
	case UpdateKindAppOnly:
		return errors.New("app-only updates require a gadget binary, not an image")
	default:
		return fmt.Errorf("unsupported update kind: %s", kind)
	}

	return nil
}

func deviceFlavour(devicePath string) (string, error) {
	d, _, _, appPartition, err := loadImage(devicePath, diskfs.ReadOnly)
	if err != nil {
		return "", err
	}
	defer d.Close()

	fs, err := filesystemForPartition(d, appPartition)
	if err != nil {
		return "", err
	}

	flavour, err := readImageFlavour(fs)
	if err != nil {
		return "", err
	}
	if flavour != "" {
		return flavour, nil
	}

	tbl, err := d.GetPartitionTable()
	if err != nil {
		return "", err
	}

	fallback := flavourFromTable(tbl)
	if fallback == "" {
		return "", errors.New("unable to determine image flavour")
	}
	return fallback, nil
}

func flavourFromTable(t partition.Table) string {
	switch t.(type) {
	case *gpt.Table:
		return "radxa_zero3"
	case *mbr.Table:
		return "raspberry_pi"
	default:
		return ""
	}
}

func readImageFlavour(fs filesystem.FileSystem) (string, error) {
	f, err := fs.OpenFile("/.image-flavour", os.O_RDONLY)
	if err != nil {
		// Some filesystems return a custom error string rather than os.ErrNotExist; treat any failure as "missing".
		return "", nil
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	flavour := strings.TrimSpace(string(data))
	if !validFlavours[flavour] {
		return "", nil
	}
	return flavour, nil
}
