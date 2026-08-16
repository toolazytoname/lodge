//go:build linux

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	operatorPolicyPath = "/etc/lodge-agent/operator.json"
	operatorBackupDir  = "/var/lib/lodge-agent/operator-backups"
	operatorCmdTimeout = 10 * time.Second
	minimumOwnerUID    = 1000
)

type operatorAccount struct {
	Name string
	UID  uint32
	Home string
}

type operatorUnitInfo struct {
	ID          string
	User        string
	LoadState   string
	ActiveState string
}

type operatorCommandRunner func(name string, args ...string) ([]byte, error)

func WriteOperatorOwners(writer io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("operator policy reader must run as root through the exact sudoers rule")
	}
	return writeOperatorOwners(operatorPolicyPath, 0, writer)
}

func ExecuteOperator(reader io.Reader, writer io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("operator executor must run as root through the exact sudoers rule")
	}
	return executeOperator(operatorPolicyPath, operatorBackupDir, 0, reader, writer, runOperatorCommand)
}

func writeOperatorOwners(policyPath string, expectedUID uint32, writer io.Writer) error {
	policy, err := loadOperatorPolicyFile(policyPath, expectedUID)
	if err != nil {
		return err
	}
	return json.NewEncoder(writer).Encode(operatorListResponse{
		Owners:     append([]string{}, policy.Owners...),
		Operations: append([]string{}, operatorOperations...),
	})
}

func executeOperator(policyPath, backupDir string, expectedUID uint32, reader io.Reader, writer io.Writer, runner operatorCommandRunner) error {
	request, err := decodeOperatorRequest(reader)
	if err != nil {
		return err
	}
	policy, err := loadOperatorPolicyFile(policyPath, expectedUID)
	if err != nil {
		return err
	}
	if !ownerApproved(policy, request.Owner) {
		return errors.New("operator owner is not approved by root policy")
	}
	account, err := lookupOperatorAccount(request.Owner)
	if err != nil {
		return err
	}
	result, err := performOperatorRequest(account, request, backupDir, runner)
	if err != nil {
		return err
	}
	return json.NewEncoder(writer).Encode(result)
}

func loadOperatorPolicyFile(path string, expectedUID uint32) (operatorPolicy, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return emptyOperatorPolicy(), nil
	}
	if err != nil {
		return operatorPolicy{}, errors.New("operator policy cannot be opened safely")
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return operatorPolicy{}, errors.New("operator policy file handle is invalid")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return operatorPolicy{}, errors.New("operator policy metadata is unavailable")
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != expectedUID || stat.Mode&0o777 != 0o600 {
		return operatorPolicy{}, errors.New("operator policy must be a root-owned regular file with mode 0600")
	}
	content, err := io.ReadAll(io.LimitReader(file, maximumOperatorPolicy+1))
	if err != nil {
		return operatorPolicy{}, errors.New("operator policy read failed")
	}
	return decodeOperatorPolicy(content)
}

func lookupOperatorAccount(name string) (operatorAccount, error) {
	if err := validateOwnerName(name); err != nil {
		return operatorAccount{}, err
	}
	account, err := user.Lookup(name)
	if err != nil {
		return operatorAccount{}, errors.New("operator owner does not exist")
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid < minimumOwnerUID {
		return operatorAccount{}, errors.New("operator owner is not a non-root login account")
	}
	if account.Username != name {
		return operatorAccount{}, errors.New("operator owner name does not match the local account")
	}
	home := path.Clean(account.HomeDir)
	if !approvedOwnerHome(home) {
		return operatorAccount{}, errors.New("operator owner home is not allowed")
	}
	return operatorAccount{Name: name, UID: uint32(uid), Home: home}, nil
}

func performOperatorRequest(account operatorAccount, request operatorRequest, backupDir string, runner operatorCommandRunner) (operatorResult, error) {
	result := operatorResult{Owner: account.Name, Op: request.Op}
	switch request.Op {
	case operatorOpReadFile:
		content, sum, err := readOwnerFile(account, request.Path)
		if err != nil {
			return operatorResult{}, err
		}
		result.OK = true
		result.Path = request.Path
		result.Content = content
		result.SHA256 = sum
		result.Summary = "read existing owner file"
		return result, nil
	case operatorOpWriteFile:
		sum, err := writeOwnerFile(account, request.Path, request.Content, request.SHA256, backupDir)
		if err != nil {
			return operatorResult{}, err
		}
		result.OK = true
		result.Path = request.Path
		result.SHA256 = sum
		result.Summary = "replaced existing owner file"
		return result, nil
	case operatorOpListDir:
		entries, err := listOwnerDir(account, request.Path)
		if err != nil {
			return operatorResult{}, err
		}
		result.OK = true
		result.Path = request.Path
		result.Entries = entries
		result.Summary = fmt.Sprintf("listed %d owner directory entries", len(entries))
		return result, nil
	case operatorOpUnitStatus, operatorOpUnitRestart:
		info, err := inspectOwnerUnit(account, request.Unit, runner)
		if err != nil {
			return operatorResult{}, err
		}
		if request.Op == operatorOpUnitRestart {
			if _, err := runner("systemctl", "restart", "--", request.Unit); err != nil {
				return operatorResult{}, errors.New("operator unit restart failed")
			}
			info, err = inspectOwnerUnit(account, request.Unit, runner)
			if err != nil {
				return operatorResult{}, err
			}
			result.Summary = "restarted owner unit"
		} else {
			result.Summary = "read owner unit status"
		}
		result.OK = true
		result.Unit = request.Unit
		result.Active = info.ActiveState
		result.User = info.User
		return result, nil
	default:
		return operatorResult{}, errors.New("operator operation is not supported")
	}
}

func readOwnerFile(account operatorAccount, rel string) (string, string, error) {
	fd, err := openUnderHome(account.Home, rel, unix.O_RDONLY)
	if err != nil {
		return "", "", err
	}
	file := os.NewFile(uintptr(fd), rel)
	if file == nil {
		_ = unix.Close(fd)
		return "", "", errors.New("operator file handle is invalid")
	}
	defer file.Close()
	if err := requireOwnerRegularFile(fd, account.UID); err != nil {
		return "", "", err
	}
	content, err := io.ReadAll(io.LimitReader(file, maximumOperatorFile+1))
	if err != nil {
		return "", "", errors.New("operator file read failed")
	}
	if len(content) > maximumOperatorFile {
		return "", "", errors.New("operator file exceeds 256 KiB")
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return "", "", errors.New("operator file content is invalid")
	}
	return string(content), sha256Hex(content), nil
}

func writeOwnerFile(account operatorAccount, rel, content, cas, backupDir string) (string, error) {
	fd, err := openUnderHome(account.Home, rel, unix.O_RDWR)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), rel)
	if file == nil {
		_ = unix.Close(fd)
		return "", errors.New("operator file handle is invalid")
	}
	defer file.Close()
	if err := requireOwnerRegularFile(fd, account.UID); err != nil {
		return "", err
	}
	current, err := io.ReadAll(io.LimitReader(file, maximumOperatorFile+1))
	if err != nil {
		return "", errors.New("operator file read failed")
	}
	if len(current) > maximumOperatorFile {
		return "", errors.New("operator file exceeds 256 KiB")
	}
	currentSum := sha256Hex(current)
	if cas != "" && cas != currentSum {
		return "", errors.New("operator file sha256 does not match")
	}
	if err := writeOperatorBackup(backupDir, account.Name, rel, current); err != nil {
		return "", err
	}
	if err := unix.Ftruncate(fd, 0); err != nil {
		return "", errors.New("operator file truncate failed")
	}
	if _, err := unix.Seek(fd, 0, 0); err != nil {
		return "", errors.New("operator file seek failed")
	}
	payload := []byte(content)
	if _, err := unix.Write(fd, payload); err != nil {
		return "", errors.New("operator file write failed")
	}
	if err := unix.Fsync(fd); err != nil {
		return "", errors.New("operator file sync failed")
	}
	return sha256Hex(payload), nil
}

func listOwnerDir(account operatorAccount, rel string) ([]string, error) {
	fd, err := openUnderHome(account.Home, rel, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), rel)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("operator directory handle is invalid")
	}
	defer file.Close()
	if err := requireOwnerDirectory(fd, account.UID); err != nil {
		return nil, err
	}
	entries, err := file.ReadDir(0)
	if err != nil {
		return nil, errors.New("operator directory read failed")
	}
	if len(entries) > maximumOperatorListEntries {
		return nil, errors.New("operator directory exceeds entry limit")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || !utf8.ValidString(name) {
			return nil, errors.New("operator directory entry is invalid")
		}
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func requireOwnerRegularFile(fd int, ownerUID uint32) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return errors.New("operator file metadata is unavailable")
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != ownerUID {
		return errors.New("operator path must be an existing regular file owned by the approved user")
	}
	return nil
}

func requireOwnerDirectory(fd int, ownerUID uint32) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return errors.New("operator directory metadata is unavailable")
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != ownerUID {
		return errors.New("operator path must be an existing directory owned by the approved user")
	}
	return nil
}

func openUnderHome(home, rel string, flags int) (int, error) {
	dirfd, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, errors.New("owner home cannot be opened safely")
	}
	if rel == "" {
		if flags&unix.O_DIRECTORY == 0 {
			_ = unix.Close(dirfd)
			return -1, errors.New("operator path is required")
		}
		return dirfd, nil
	}
	parts := strings.Split(rel, "/")
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			_ = unix.Close(dirfd)
			return -1, errors.New("operator path is invalid")
		}
		nextFlags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if index == len(parts)-1 {
			nextFlags = flags | unix.O_CLOEXEC | unix.O_NOFOLLOW
		}
		next, err := unix.Openat(dirfd, part, nextFlags, 0)
		_ = unix.Close(dirfd)
		if err != nil {
			return -1, errors.New("operator path cannot be opened safely")
		}
		dirfd = next
	}
	return dirfd, nil
}

func writeOperatorBackup(backupDir, owner, rel string, content []byte) error {
	if backupDir == "" {
		return errors.New("operator backup directory is missing")
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return errors.New("operator backup directory cannot be created")
	}
	dirfd, err := unix.Open(backupDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("operator backup directory cannot be opened safely")
	}
	defer unix.Close(dirfd)
	var stat unix.Stat_t
	if err := unix.Fstat(dirfd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o077 != 0 {
		return errors.New("operator backup directory is unsafe")
	}
	base := filepath.Base(rel)
	if base == "." || base == "/" || base == "" {
		base = "file"
	}
	name := time.Now().UTC().Format("20060102T150405Z") + "-" + owner + "-" + sha256Hex([]byte(rel))[:16] + "-" + base
	backupPath := filepath.Join(backupDir, name)
	if !strings.HasPrefix(backupPath, backupDir+string(os.PathSeparator)) {
		return errors.New("operator backup path is invalid")
	}
	fd, err := unix.Open(backupPath, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return errors.New("operator backup cannot be created")
	}
	defer unix.Close(fd)
	if _, err := unix.Write(fd, content); err != nil {
		return errors.New("operator backup write failed")
	}
	if err := unix.Fsync(fd); err != nil {
		return errors.New("operator backup sync failed")
	}
	return nil
}

func inspectOwnerUnit(account operatorAccount, unit string, runner operatorCommandRunner) (operatorUnitInfo, error) {
	if deniedSystemUnit(unit) {
		return operatorUnitInfo{}, errors.New("operator unit is a denied system service")
	}
	output, err := runner("systemctl", "show", "--property=Id,User,LoadState,ActiveState", "--", unit)
	if err != nil {
		return operatorUnitInfo{}, errors.New("operator unit status failed")
	}
	info := parseSystemctlShow(output)
	if info.ID != unit {
		return operatorUnitInfo{}, errors.New("operator unit identity does not match")
	}
	if info.LoadState != "loaded" {
		return operatorUnitInfo{}, errors.New("operator unit is not loaded")
	}
	if info.User != account.Name {
		return operatorUnitInfo{}, errors.New("operator unit User= does not match the approved owner")
	}
	if deniedSystemUnit(info.ID) {
		return operatorUnitInfo{}, errors.New("operator unit is a denied system service")
	}
	return info, nil
}

func parseSystemctlShow(output []byte) operatorUnitInfo {
	var info operatorUnitInfo
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "Id":
			info.ID = value
		case "User":
			info.User = value
		case "LoadState":
			info.LoadState = value
		case "ActiveState":
			info.ActiveState = value
		}
	}
	return info
}

func runOperatorCommand(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), operatorCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return output, nil
}
