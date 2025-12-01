package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/partition"
	"github.com/diskfs/go-diskfs/partition/gpt"
	"github.com/diskfs/go-diskfs/partition/mbr"
	"github.com/tez-capital/tezsign/tools/constants"
)

type UpdateKind string

const (
	UpdateKindFull    UpdateKind = "full"
	UpdateKindAppOnly UpdateKind = "app"
)

func main() {
	logger := slog.Default()

	var source string
	var appBinary string
	var sourceProvided bool
	if len(os.Args) >= 2 {
		source = os.Args[1]
		sourceProvided = true
	}

	// Keep the previous non-interactive flow when destination is provided explicitly.
	if sourceProvided && len(os.Args) >= 3 {
		destination := os.Args[2]
		kind := UpdateKindFull
		if len(os.Args) >= 4 {
			kind = UpdateKind(os.Args[3])
			switch kind {
			case UpdateKindFull, UpdateKindAppOnly:
			default:
				logger.Error("Invalid update kind. Valid options are: full, app")
				os.Exit(1)
			}
		}

		switch kind {
		case UpdateKindFull:
			if err := performUpdate(source, destination, kind, logger); err != nil {
				logger.Error("Update failed", "error", err)
				os.Exit(1)
			}
		case UpdateKindAppOnly:
			appBinary = source
			if err := performAppBinaryUpdate(appBinary, destination, logger); err != nil {
				logger.Error("Update failed", "error", err)
				os.Exit(1)
			}
		default:
			logger.Error("Invalid update kind. Valid options are: full, app")
			os.Exit(1)
		}

		logger.Info("Update completed successfully")
		return
	}

	devices, err := discoverTezsignDevices(logger)
	if err != nil {
		logger.Error("Failed to discover TezSign devices", "error", err)
		os.Exit(1)
	}

	selectedDevice, kind, err := runSelection(devices)
	if err != nil {
		logger.Error("Selection failed", "error", err)
		os.Exit(1)
	}

	if !sourceProvided {
		switch kind {
		case UpdateKindFull:
			flavour, err := deviceFlavour(selectedDevice.Path)
			if err != nil {
				logger.Error("Failed to detect device flavor", "error", err)
				os.Exit(1)
			}
			url := fmt.Sprintf("%s%s.img.xz", constants.LatestReleaseURL, flavour)
			downloaded, cleanupFn, err := downloadWithProgress(url)
			if err != nil {
				logger.Error("Failed to download image", "error", err)
				os.Exit(1)
			}
			defer cleanupFn()
			source = downloaded
		case UpdateKindAppOnly:
			url := fmt.Sprintf("%s%s", constants.LatestReleaseURL, constants.AppBinaryName)
			downloaded, cleanupFn, err := downloadWithProgress(url)
			if err != nil {
				logger.Error("Failed to download gadget binary", "error", err)
				os.Exit(1)
			}
			defer cleanupFn()
			appBinary = downloaded
		default:
			logger.Error("Unsupported update kind", "kind", kind)
			os.Exit(1)
		}
	}

	switch kind {
	case UpdateKindFull:
		if _, err := os.Stat(source); err != nil {
			logger.Error("Invalid source image", "error", err)
			os.Exit(1)
		}
	case UpdateKindAppOnly:
		if _, err := os.Stat(appBinary); err != nil {
			logger.Error("Invalid gadget binary", "error", err)
			os.Exit(1)
		}
	}

	fmt.Printf("Updating %s with a %s update...\n\n", selectedDevice.Path, string(kind))

	switch kind {
	case UpdateKindFull:
		if err := performUpdate(source, selectedDevice.Path, kind, logger); err != nil {
			logger.Error("Update failed", "error", err)
			os.Exit(1)
		}
	case UpdateKindAppOnly:
		if err := performAppBinaryUpdate(appBinary, selectedDevice.Path, logger); err != nil {
			logger.Error("Update failed", "error", err)
			os.Exit(1)
		}
	default:
		logger.Error("Unsupported update kind", "kind", kind)
		os.Exit(1)
	}

	fmt.Println("✅ Update completed successfully")
}

func readSysfsValue(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func readBlockSizeBytes(name string) uint64 {
	sizeContent := readSysfsValue(filepath.Join("/sys/block", name, "size"))
	sectors, err := strconv.ParseUint(strings.TrimSpace(sizeContent), 10, 64)
	if err != nil {
		return 0
	}
	return sectors * 512 // sectors are 512-byte blocks
}

func hasExpectedPartitionCount(t partition.Table) bool {
	switch tt := t.(type) {
	case *gpt.Table:
		return len(tt.Partitions) >= 3
	case *mbr.Table:
		nonZero := 0
		for _, p := range tt.Partitions {
			if p != nil && p.Size > 0 {
				nonZero++
			}
		}
		return nonZero == 4
	default:
		return false
	}
}

func checkTezsignMarker(disk *disk.Disk) (bool, error) {
	table, err := disk.GetPartitionTable()
	if err != nil {
		return false, err
	}
	if !hasExpectedPartitionCount(table) {
		return false, nil
	}
	hasApp := false
	hasData := false
	for idx := range table.GetPartitions() {
		fs, err := disk.GetFilesystem(idx + 1)
		if err == nil {
			label := strings.TrimSpace(fs.Label())
			if label == constants.AppPartitionLabel {
				if _, err := fs.OpenFile("/tezsign", os.O_RDONLY); err == nil {
					hasApp = true
				}
			}
			if label == constants.DataPartitionLabel {
				hasData = true
			}
		}
	}
	return hasApp && hasData, nil
}

func probeTezsignDevice(path string) (bool, string) {
	disk, _, _, _, err := loadImage(path, diskfs.ReadOnly)
	if err != nil {
		return false, err.Error()
	}
	defer disk.Close()

	ok, err := checkTezsignMarker(disk)
	switch {
	case err != nil:
		return false, "marker check failed"
	case !ok:
		return false, "device does not match TezSign layout"
	default:
		return true, "OK"
	}
}

func discoverTezsignDevices(logger *slog.Logger) ([]deviceCandidate, error) {
	paths, err := filepath.Glob("/sys/block/*/removable")
	if err != nil {
		return nil, fmt.Errorf("failed to list block devices: %w", err)
	}

	var devices []deviceCandidate
	for _, removablePath := range paths {
		flag, err := os.ReadFile(removablePath)
		if err != nil || strings.TrimSpace(string(flag)) != "1" {
			continue
		}

		name := filepath.Base(filepath.Dir(removablePath))
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
			continue
		}

		devicePath := filepath.Join("/dev", name)
		sizeBytes := readBlockSizeBytes(name)
		model := strings.TrimSpace(readSysfsValue(filepath.Join("/sys/block", name, "device/model")))

		isTezsign, status := probeTezsignDevice(devicePath)
		if !isTezsign {
			logger.Debug("Device did not validate as TezSign", "device", devicePath, "status", status)
		}

		devices = append(devices, deviceCandidate{
			Name:      name,
			Path:      devicePath,
			SizeBytes: sizeBytes,
			Model:     model,
			Status:    status,
			Valid:     isTezsign,
		})
	}

	if len(devices) == 0 {
		return nil, errors.New("no removable block devices detected")
	}

	return devices, nil
}

func runSelection(devices []deviceCandidate) (deviceCandidate, UpdateKind, error) {
	program := tea.NewProgram(newSelectionModel(devices))
	model, err := program.Run()
	if err != nil {
		return deviceCandidate{}, "", err
	}

	selection, ok := model.(selectionModel)
	if !ok {
		return deviceCandidate{}, "", errors.New("failed to read selection state")
	}

	if selection.err != nil {
		return deviceCandidate{}, "", selection.err
	}

	if selection.selectedDevice == nil {
		return deviceCandidate{}, "", errors.New("no device selected")
	}

	return *selection.selectedDevice, selection.selectedKind, nil
}

func downloadWithProgress(url string) (string, func(), error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", nil, fmt.Errorf("failed to download image: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return "", nil, fmt.Errorf("failed to download image: %s", resp.Status)
	}

	tmpFile, err := os.CreateTemp("", "tezsign_download_*.img.xz")
	if err != nil {
		resp.Body.Close()
		return "", nil, fmt.Errorf("failed to create temp file for download: %w", err)
	}

	total := resp.ContentLength
	cr := &countingReader{r: resp.Body}
	cancel := func() {
		resp.Body.Close()
		tmpFile.Close()
		os.Remove(tmpFile.Name())
	}

	title := fmt.Sprintf("Download %s → %s", filepath.Base(url), filepath.Base(tmpFile.Name()))
	p := tea.NewProgram(newProgressModel(title, total, cr, cancel))

	go func() {
		_, copyErr := io.Copy(tmpFile, cr)
		tmpFile.Close()
		resp.Body.Close()
		p.Send(finishMsg{err: copyErr})
	}()

	model, progErr := p.Run()
	if progErr != nil {
		cancel()
		return "", nil, fmt.Errorf("failed to render download progress: %w", progErr)
	}

	res, ok := model.(progressModel)
	if !ok {
		cancel()
		return "", nil, errors.New("unexpected model type after download")
	}

	if res.err != nil {
		cancel()
		return "", nil, fmt.Errorf("failed to download image: %w", res.err)
	}

	cleanup := func() {
		os.Remove(tmpFile.Name())
	}

	return tmpFile.Name(), cleanup, nil
}
