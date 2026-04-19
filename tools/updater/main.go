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
	"github.com/tez-capital/tezsign/logging"
	"github.com/tez-capital/tezsign/tools/constants"
)

type UpdateKind string

const (
	UpdateKindFull UpdateKind = "full"
)

func main() {
	logger, _ := logging.NewFromEnv()

	args := os.Args[1:]
	if hasHelpFlag(args) {
		printUsage()
		return
	}

	var source string
	var sourceProvided bool
	if len(args) >= 1 {
		source = args[0]
		sourceProvided = true
	}

	// Keep the previous non-interactive flow when destination is provided explicitly.
	if sourceProvided && len(args) >= 2 {
		destination := args[1]
		if len(args) >= 3 {
			kind := UpdateKind(args[2])
			if kind != UpdateKindFull {
				logger.Error("Invalid update kind. Valid option is: full")
				os.Exit(1)
			}
		}

		if err := performUpdate(source, destination, UpdateKindFull, logger); err != nil {
			logger.Error("Update failed", "error", err)
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

	selectedDevice, err := runSelection(devices)
	if err != nil {
		logger.Error("Selection failed", "error", err)
		os.Exit(1)
	}

	if !sourceProvided {
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
	}

	if _, err := os.Stat(source); err != nil {
		logger.Error("Invalid source image", "error", err)
		os.Exit(1)
	}

	fmt.Printf("Updating %s with a %s update...\n\n", selectedDevice.Path, string(UpdateKindFull))

	if err := performUpdate(source, selectedDevice.Path, UpdateKindFull, logger); err != nil {
		logger.Error("Update failed", "error", err)
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

func checkTezsignMarker(disk *disk.Disk) (bool, error) {
	table, err := disk.GetPartitionTable()
	if err != nil {
		return false, err
	}
	nonZeroPartitions := 0
	for _, p := range table.GetPartitions() {
		if p != nil && p.GetSize() > 0 {
			nonZeroPartitions++
		}
	}
	if nonZeroPartitions != 3 {
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

func runSelection(devices []deviceCandidate) (deviceCandidate, error) {
	program := tea.NewProgram(newSelectionModel(devices))
	model, err := program.Run()
	if err != nil {
		return deviceCandidate{}, err
	}

	selection, ok := model.(selectionModel)
	if !ok {
		return deviceCandidate{}, errors.New("failed to read selection state")
	}

	if selection.err != nil {
		return deviceCandidate{}, selection.err
	}

	if selection.selectedDevice == nil {
		return deviceCandidate{}, errors.New("no device selected")
	}

	return *selection.selectedDevice, nil
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

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "-help" || arg == "--help" {
			return true
		}
	}
	return false
}

func printUsage() {
	bin := filepath.Base(os.Args[0])
	fmt.Printf(`TezSign Updater

Usage:
  %[1]s
      Interactive mode: pick a device and download the latest release automatically.
  %[1]s <source>
      Interactive mode using a local image; destination is still selected interactively.
  %[1]s <source> <destination> [full]
      Non-interactive full update using a local image.

Options:
  -h, --help    Show this help message.
`, bin)
}
