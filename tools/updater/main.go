package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/partition/part"
	"github.com/samber/lo"
	"github.com/tez-capital/tezsign/tools/common"
)

type UpdateKind string

const (
	UpdateKindFull    UpdateKind = "full"
	UpdateKindAppOnly UpdateKind = "app"
)

type selectionStage int

const (
	stageDevice selectionStage = iota
	stageKind
)

type deviceCandidate struct {
	Name      string
	Path      string
	SizeBytes uint64
	Model     string
	Status    string
	Valid     bool
}

type selectionModel struct {
	stage          selectionStage
	devices        []deviceCandidate
	table          table.Model
	kindCursor     int
	selectedDevice *deviceCandidate
	selectedKind   UpdateKind
	err            error
}

func newSelectionModel(devices []deviceCandidate) selectionModel {
	columns := []table.Column{
		{Title: "Name", Width: 10},
		{Title: "Size", Width: 12},
		{Title: "Status", Width: 22},
		{Title: "Model", Width: 16},
		{Title: "Path", Width: 20},
	}

	rows := make([]table.Row, 0, len(devices))
	for _, dev := range devices {
		rows = append(rows, table.Row{
			dev.Name,
			byteCountToHumanReadable(int64(dev.SizeBytes)),
			dev.Status,
			dev.Model,
			dev.Path,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(10),
	)
	t.SetStyles(newTableStyles())

	return selectionModel{
		stage:        stageDevice,
		devices:      devices,
		table:        t,
		selectedKind: UpdateKindFull,
	}
}

func newTableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	return s
}

func (m selectionModel) Init() tea.Cmd {
	return nil
}

func (m selectionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.err = errors.New("update cancelled")
			return m, tea.Quit
		}

		switch m.stage {
		case stageDevice:
			switch msg.String() {
			case "up", "k":
				m.table.MoveUp(1)
			case "down", "j":
				m.table.MoveDown(1)
			case "enter":
				if len(m.devices) == 0 {
					m.err = errors.New("no TezSign SD cards detected")
					return m, tea.Quit
				}
				cursor := m.table.Cursor()
				if cursor < 0 || cursor >= len(m.devices) {
					m.err = errors.New("invalid selection")
					return m, tea.Quit
				}
				m.selectedDevice = &m.devices[cursor]
				m.stage = stageKind
			}
		case stageKind:
			switch msg.String() {
			case "up", "k":
				if m.kindCursor > 0 {
					m.kindCursor--
				}
			case "down", "j":
				if m.kindCursor < 1 {
					m.kindCursor++
				}
			case "enter":
				options := []UpdateKind{UpdateKindFull, UpdateKindAppOnly}
				m.selectedKind = options[m.kindCursor]
				return m, tea.Quit
			}
		}
	case tea.WindowSizeMsg:
		width := msg.Width - 4
		if width < 20 {
			width = 20
		}
		m.table.SetWidth(width)
	}

	return m, nil
}

func (m selectionModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	switch m.stage {
	case stageDevice:
		if len(m.devices) == 0 {
			return "No TezSign SD cards detected. Press q to exit."
		}
		return fmt.Sprintf(
			"Select a device (↑/↓ to navigate, enter to choose)\n\n%s\n\nPress enter to continue or q to cancel.",
			m.table.View(),
		)
	case stageKind:
		options := []struct {
			kind  UpdateKind
			title string
		}{
			{UpdateKindFull, "Full (boot, rootfs and app)"},
			{UpdateKindAppOnly, "TezSign app only"},
		}

		var sb strings.Builder
		sb.WriteString("Select update kind (↑/↓ to navigate, enter to start)\n\n")
		for i, option := range options {
			cursor := "  "
			if i == m.kindCursor {
				cursor = "> "
			}
			sb.WriteString(fmt.Sprintf("%s%s\n", cursor, option.title))
		}
		sb.WriteString("\nPress q to cancel.")
		return sb.String()
	default:
		return ""
	}
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

func probeTezsignDevice(path string) (bool, string) {
	disk, _, _, appPartition, err := loadImage(path, diskfs.ReadOnly)
	if err != nil {
		return false, err.Error()
	}
	defer disk.Close()

	ok, err := checkTezsignMarker(disk, appPartition)
	if err != nil {
		return true, fmt.Sprintf("marker check failed: %v", err)
	}
	if !ok {
		return true, "marker /tezsign missing (will be updated)"
	}
	return true, "OK"
}

func checkTezsignMarker(disk *disk.Disk, appPartition part.Partition) (bool, error) {
	indexOfAppPartition := lo.IndexOf(disk.Table.GetPartitions(), appPartition)
	if indexOfAppPartition == -1 {
		return false, errors.New("app partition not found")
	}

	fs, err := disk.GetFilesystem(indexOfAppPartition + 1)
	if err != nil {
		return false, errors.New("failed to get filesystem")
	}

	f, err := fs.OpenFile("/tezsign", os.O_RDONLY)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	return true, nil
}

func loadImage(path string, mode diskfs.OpenModeOption) (*disk.Disk, part.Partition, part.Partition, part.Partition, error) {
	flags := os.O_RDONLY
	if mode != diskfs.ReadOnly {
		flags = os.O_RDWR
	}

	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to open device %s: %w", path, err)
	}

	disk, err := diskfs.OpenBackend(file.New(f, false), diskfs.WithOpenMode(mode), diskfs.WithSectorSize(diskfs.SectorSizeDefault))
	if err != nil {
		f.Close()
		return nil, nil, nil, nil, errors.New("failed to open disk backend")
	}

	bootPartition, rootfsPartition, appPartition, _, err := common.GetTezsignPartitions(disk)
	if err != nil {
		disk.Close()
		return nil, nil, nil, nil, fmt.Errorf("failed to read partitions from the device: %w", err)
	}

	return disk, bootPartition, rootfsPartition, appPartition, nil
}

func copyPartitionData(srcDisk *disk.Disk, srcPartition part.Partition, dstDisk *disk.Disk, dstPartition part.Partition, logger *slog.Logger) error {
	pr, pw := io.Pipe()
	writableDst, err := dstDisk.Backend.Writable()
	if err != nil {
		return errors.New("failed to get writable backend for destination disk")
	}

	progressLogger := NewProgressLogger(pw, logger)

	var wg sync.WaitGroup
	var readErr, writeErr error
	var readBytes int64

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer pw.Close() // Close the writer side of the pipe when done

		// ReadContents(backend, out io.Writer) streams data FROM the partition TO the provided writer (pw).
		readBytes, readErr = srcPartition.ReadContents(srcDisk.Backend, progressLogger)
		if readErr != nil {
			logger.Error("Failed to read contents from source partition", "error", readErr)
			return
		}
	}()

	writtenBytes, writeErr := dstPartition.WriteContents(writableDst, pr)
	if writeErr != nil {
		logger.Error("Failed to write contents to destination partition", "error", writeErr)
	}
	pr.Close()
	wg.Wait()

	if readErr != nil {
		return errors.New("error occurred while reading from source partition: " + readErr.Error())
	}
	if writeErr != nil {
		return errors.New("error occurred while writing to destination partition: " + writeErr.Error())
	}
	if uint64(readBytes) != writtenBytes {
		return errors.New("mismatch in bytes read and written")
	}
	return nil
}

func performUpdate(source, destination string, kind UpdateKind, logger *slog.Logger) error {
	logger.Info("Starting TezSign updater", "source", source, "destination", destination, "kind", string(kind))

	sourceImg, sourceBootPartition, sourceRootfsPartition, sourceAppPartition, err := loadImage(source, diskfs.ReadOnly)
	if err != nil {
		return fmt.Errorf("failed to load source image: %w", err)
	}
	defer sourceImg.Close()

	dstImg, destinationBootPartition, destinationRootfsPartition, destinationAppPartition, err := loadImage(destination, diskfs.ReadWriteExclusive)
	if err != nil {
		return fmt.Errorf("failed to load destination image: %w", err)
	}
	defer dstImg.Close()

	if ok, err := checkTezsignMarker(dstImg, destinationAppPartition); err != nil {
		logger.Debug("Skipping marker check", "error", err)
	} else if !ok {
		logger.Warn("Destination missing /tezsign marker; proceeding and will overwrite app partition")
	}

	switch kind {
	case UpdateKindFull:
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
			if err = copyPartitionData(sourceImg, sourceBootPartition, dstImg, destinationBootPartition, logger); err != nil {
				return fmt.Errorf("failed to update boot partition: %w", err)
			}
		}

		logger.Info("Updating rootfs partition...")
		if err = copyPartitionData(sourceImg, sourceRootfsPartition, dstImg, destinationRootfsPartition, logger); err != nil {
			return fmt.Errorf("failed to update rootfs partition: %w", err)
		}

		logger.Info("Updating app partition...")
		if err = copyPartitionData(sourceImg, sourceAppPartition, dstImg, destinationAppPartition, logger); err != nil {
			return fmt.Errorf("failed to update app partition: %w", err)
		}
	case UpdateKindAppOnly:
		logger.Info("Updating app partition...")
		if err = copyPartitionData(sourceImg, sourceAppPartition, dstImg, destinationAppPartition, logger); err != nil {
			return fmt.Errorf("failed to update app partition: %w", err)
		}
	default:
		return fmt.Errorf("unsupported update kind: %s", kind)
	}

	return nil
}

func main() {
	logger := slog.Default()

	if len(os.Args) < 2 {
		logger.Error("Usage: tezsign_updater <source_img> [destination_device] [update_kind]")
		os.Exit(1)
	}

	source := os.Args[1]
	if _, err := os.Stat(source); err != nil {
		logger.Error("Invalid source image", "error", err)
		os.Exit(1)
	}

	// Keep the previous non-interactive flow when destination is provided explicitly.
	if len(os.Args) >= 3 {
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

		if err := performUpdate(source, destination, kind, logger); err != nil {
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

	selectedDevice, kind, err := runSelection(devices)
	if err != nil {
		logger.Error("Selection failed", "error", err)
		os.Exit(1)
	}

	fmt.Printf("Updating %s with a %s update...\n\n", selectedDevice.Path, string(kind))

	if err := performUpdate(source, selectedDevice.Path, kind, logger); err != nil {
		logger.Error("Update failed", "error", err)
		os.Exit(1)
	}

	fmt.Println("✅ Update completed successfully")
}
