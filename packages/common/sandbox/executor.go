package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

const (
	PolicyDangerousCommand = "dangerous_command"

	defaultArtifactOutputDir       = "output"
	defaultMaxArtifacts            = 32
	defaultMaxArtifactBytes  int64 = 10 * 1024 * 1024
	defaultMaxArtifactTotal  int64 = 50 * 1024 * 1024
)

type Executor struct {
	AllowedCommands  []string
	Dangerous        []string
	WorkspaceRoot    string
	MaxOutputBytes   int
	ArtifactOutput   string
	MaxArtifacts     int
	MaxArtifactBytes int64
	MaxArtifactTotal int64
}

func NewExecutor() *Executor {
	return &Executor{
		AllowedCommands:  []string{"curl", "wget", "dig", "nslookup", "date", "echo", "pwd", "ls"},
		Dangerous:        []string{"rm", "sudo", "chmod", "chown", "ssh", "scp", "mv", "cp", "mkdir", "touch", "tee", "sh", "bash"},
		WorkspaceRoot:    "workspaces",
		MaxOutputBytes:   64 * 1024,
		ArtifactOutput:   defaultArtifactOutputDir,
		MaxArtifacts:     defaultMaxArtifacts,
		MaxArtifactBytes: defaultMaxArtifactBytes,
		MaxArtifactTotal: defaultMaxArtifactTotal,
	}
}

func (e *Executor) Execute(ctx context.Context, request protocol.SandboxCommand) protocol.SandboxResult {
	start := time.Now()
	result := protocol.SandboxResult{RunID: request.RunID, Command: append([]string(nil), request.Command...)}
	if len(request.Command) == 0 {
		result.ExitCode = -1
		result.Error = "empty command"
		result.Duration = time.Since(start).String()
		return result
	}
	if err := e.validate(request); err != nil {
		result.ExitCode = -1
		result.Error = err.Error()
		if errors.Is(err, ErrDangerousCommand) {
			result.Reason = "command is blocked by the system CLI read-only policy"
			result.Policy = PolicyDangerousCommand
		}
		result.Duration = time.Since(start).String()
		return result
	}
	timeout := time.Duration(request.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	workspace, err := e.workspaceDir(request)
	if err != nil {
		result.ExitCode = -1
		result.Error = err.Error()
		result.Duration = time.Since(start).String()
		return result
	}
	before, err := e.snapshotOutput(workspace, request)
	if err != nil {
		result.ExitCode = -1
		result.Error = err.Error()
		result.Duration = time.Since(start).String()
		return result
	}
	cmd := exec.CommandContext(cmdCtx, request.Command[0], request.Command[1:]...)
	cmd.Dir = workspace
	cmd.Env = filteredEnv(request.Env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	result.Stdout, result.Truncated = truncate(stdout.String(), e.outputLimit(request))
	var stderrTruncated bool
	result.Stderr, stderrTruncated = truncate(stderr.String(), e.outputLimit(request))
	result.Truncated = result.Truncated || stderrTruncated
	artifacts, diff, scanErr := e.collectArtifacts(workspace, before, request)
	result.Artifacts = artifacts
	result.WorkspaceDiff = diff
	result.Duration = time.Since(start).String()
	if cmdCtx.Err() != nil {
		result.ExitCode = -1
		result.Error = cmdCtx.Err().Error()
		return result
	}
	if scanErr != nil {
		result.ExitCode = -1
		result.Error = scanErr.Error()
		return result
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
		result.Error = err.Error()
		return result
	}
	result.ExitCode = 0
	return result
}

func (e *Executor) validate(request protocol.SandboxCommand) error {
	name := request.Command[0]
	if strings.Contains(name, "/") {
		return errors.New("absolute or relative command paths are not allowed in the local executor")
	}
	if slices.Contains(e.Dangerous, name) {
		return fmt.Errorf("%w: command is blocked by the system CLI read-only policy", ErrDangerousCommand)
	}
	if !slices.Contains(e.AllowedCommands, name) {
		return errors.New("command is not allowed by the local executor policy")
	}
	return nil
}

var ErrDangerousCommand = errors.New("dangerous command")

func (e *Executor) workspaceDir(request protocol.SandboxCommand) (string, error) {
	root := request.WorkspaceRoot
	if root == "" {
		root = e.WorkspaceRoot
	}
	if root == "" {
		root = "workspaces"
	}
	if request.WorkspaceID == "" {
		return "", errors.New("workspace_id is required")
	}
	workspaceID := filepath.Clean(request.WorkspaceID)
	if filepath.IsAbs(request.WorkspaceID) || workspaceID == "." || workspaceID == ".." || strings.HasPrefix(workspaceID, ".."+string(os.PathSeparator)) || strings.ContainsAny(workspaceID, `/\`) {
		return "", errors.New("workspace_id must be a single relative path segment")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return "", err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(rootReal, workspaceID)
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("workspace path must not be a symlink")
	}
	if !pathWithin(rootReal, dir) {
		return "", errors.New("workspace path escapes workspace root")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dirReal, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if !pathWithin(rootReal, dirReal) {
		return "", errors.New("workspace path escapes workspace root")
	}
	if err := e.ensureWorkspaceLayout(dirReal); err != nil {
		return "", err
	}
	return dirReal, nil
}

func (e *Executor) outputLimit(request protocol.SandboxCommand) int {
	if request.MaxOutputBytes > 0 {
		return request.MaxOutputBytes
	}
	if e.MaxOutputBytes > 0 {
		return e.MaxOutputBytes
	}
	return 64 * 1024
}

func truncate(value string, limit int) (string, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	return value[:limit] + "\n[output truncated]", true
}

func filteredEnv(input map[string]string) []string {
	env := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=/tmp"}
	for key, value := range input {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.HasPrefix(key, "SECRET_") {
			continue
		}
		env = append(env, key+"="+value)
	}
	return env
}

func (e *Executor) ensureWorkspaceLayout(workspace string) error {
	for _, name := range []string{"input", e.artifactOutputDir(), "tmp"} {
		dir := filepath.Join(workspace, name)
		if info, err := os.Lstat(dir); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("workspace subdirectory %q must not be a symlink", name)
			}
			if !info.IsDir() {
				return fmt.Errorf("workspace subdirectory %q is not a directory", name)
			}
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) artifactOutputDir() string {
	name := strings.TrimSpace(e.ArtifactOutput)
	if name == "" {
		name = defaultArtifactOutputDir
	}
	clean := filepath.Clean(name)
	if filepath.IsAbs(name) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || strings.ContainsAny(clean, `/\`) {
		return defaultArtifactOutputDir
	}
	return clean
}

type artifactSnapshot struct {
	artifact protocol.Artifact
	size     int64
	sha256   string
}

func (e *Executor) snapshotOutput(workspace string, request protocol.SandboxCommand) (map[string]artifactSnapshot, error) {
	snapshots, _, err := e.scanOutput(workspace, request)
	return snapshots, err
}

func (e *Executor) collectArtifacts(workspace string, before map[string]artifactSnapshot, request protocol.SandboxCommand) ([]protocol.Artifact, protocol.SandboxWorkspaceDiff, error) {
	after, paths, err := e.scanOutput(workspace, request)
	if err != nil {
		return nil, protocol.SandboxWorkspaceDiff{}, err
	}
	var artifacts []protocol.Artifact
	diff := protocol.SandboxWorkspaceDiff{}
	for _, path := range paths {
		current := after[path]
		previous, existed := before[path]
		switch {
		case !existed:
			diff.Created = append(diff.Created, path)
			artifacts = append(artifacts, current.artifact)
		case previous.size != current.size || previous.sha256 != current.sha256:
			diff.Updated = append(diff.Updated, path)
			artifacts = append(artifacts, current.artifact)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			diff.Deleted = append(diff.Deleted, path)
		}
	}
	sort.Strings(diff.Created)
	sort.Strings(diff.Updated)
	sort.Strings(diff.Deleted)
	return artifacts, diff, nil
}

func (e *Executor) scanOutput(workspace string, request protocol.SandboxCommand) (map[string]artifactSnapshot, []string, error) {
	outputName := e.artifactOutputDir()
	outputDir := filepath.Join(workspace, outputName)
	if err := ensureNoSymlink(outputDir, outputName); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, nil, err
	}
	snapshots := map[string]artifactSnapshot{}
	var paths []string
	maxArtifacts := e.maxArtifacts()
	var totalBytes int64
	err := filepath.WalkDir(outputDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == outputDir {
			return nil
		}
		rel, err := filepath.Rel(workspace, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("artifact path %q must not be a symlink", rel)
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if len(paths) >= maxArtifacts {
			return fmt.Errorf("artifact count exceeds limit %d", maxArtifacts)
		}
		if info.Size() > e.maxArtifactBytes() {
			return fmt.Errorf("artifact %q exceeds size limit %d", rel, e.maxArtifactBytes())
		}
		totalBytes += info.Size()
		if totalBytes > e.maxArtifactTotal() {
			return fmt.Errorf("artifact total size exceeds limit %d", e.maxArtifactTotal())
		}
		artifact, err := artifactForFile(path, rel, info, request)
		if err != nil {
			return err
		}
		snapshots[rel] = artifactSnapshot{
			artifact: artifact,
			size:     info.Size(),
			sha256:   artifact.SHA256,
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(paths)
	return snapshots, paths, nil
}

func artifactForFile(path, rel string, info fs.FileInfo, request protocol.SandboxCommand) (protocol.Artifact, error) {
	hash, mimeType, err := hashAndMime(path)
	if err != nil {
		return protocol.Artifact{}, err
	}
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(path))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return protocol.Artifact{
		ID:             platform.NewID("art"),
		RunID:          request.RunID,
		WorkspaceID:    request.WorkspaceID,
		Path:           rel,
		Name:           filepath.Base(path),
		MimeType:       mimeType,
		SizeBytes:      info.Size(),
		SHA256:         hash,
		StorageBackend: "local",
		StorageKey:     path,
		CreatedAt:      time.Now().UTC(),
	}, nil
}

func hashAndMime(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	hasher := sha256.New()
	header := make([]byte, 512)
	n, readErr := file.Read(header)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", "", readErr
	}
	if n > 0 {
		if _, err := hasher.Write(header[:n]); err != nil {
			return "", "", err
		}
	}
	if _, err := io.Copy(hasher, file); err != nil {
		return "", "", err
	}
	mimeType := ""
	if n > 0 {
		mimeType = http.DetectContentType(header[:n])
	}
	return hex.EncodeToString(hasher.Sum(nil)), mimeType, nil
}

func (e *Executor) maxArtifacts() int {
	if e.MaxArtifacts > 0 {
		return e.MaxArtifacts
	}
	return defaultMaxArtifacts
}

func (e *Executor) maxArtifactBytes() int64 {
	if e.MaxArtifactBytes > 0 {
		return e.MaxArtifactBytes
	}
	return defaultMaxArtifactBytes
}

func (e *Executor) maxArtifactTotal() int64 {
	if e.MaxArtifactTotal > 0 {
		return e.MaxArtifactTotal
	}
	return defaultMaxArtifactTotal
}

func ensureNoSymlink(path, label string) error {
	info, err := os.Lstat(path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("artifact output %q must not be a symlink", label)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func pathWithin(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	return target == root || strings.HasPrefix(target, root+string(os.PathSeparator))
}
