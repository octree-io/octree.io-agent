package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type CodeExecRequest struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

var LANGUAGE_EXTENSIONS = map[string]string{
	"javascript": ".js",
	"typescript": ".ts",
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{"status": "Health check OK"}
	jsonResponse, _ := json.Marshal(response)
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonResponse)
}

func handleTypeScriptExecution(filePath string) (string, string, error) {
	stdout, stderr, err := executeTypescript(filePath)
	return string(stdout), string(stderr), err
}

// executeTypescript runs a TypeScript file by creating a unique folder and copying the necessary files
func executeTypescript(filePath string) (string, string, error) {
	// Step 1: Generate a UUID folder name
	uuidFolder := uuid.New().String()

	// Step 2: Save the current working directory
	originalDir, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("failed to get current directory: %w", err)
	}

	// Step 3: Change directory to /mnt/persistent
	err = os.Chdir("/mnt/persistent")
	if err != nil {
		return "", "", fmt.Errorf("failed to change directory to /mnt/persistent: %w", err)
	}

	// Step 4: Create a new folder with the UUID
	err = os.Mkdir(uuidFolder, os.ModePerm)
	if err != nil {
		return "", "", fmt.Errorf("failed to create folder %s: %w", uuidFolder, err)
	}

	// Step 5: Copy everything from /tmp/dummy-pkg-ts into the new UUID folder
	err = copyDirectory("/tmp/dummy-pkg-ts", filepath.Join("/mnt/persistent", uuidFolder))
	if err != nil {
		return "", "", fmt.Errorf("failed to copy files to %s: %w", uuidFolder, err)
	}

	// Step 6: Change directory to the new UUID folder
	err = os.Chdir(filepath.Join("/mnt/persistent", uuidFolder))
	if err != nil {
		return "", "", fmt.Errorf("failed to change directory to %s: %w", uuidFolder, err)
	}

	// Step 7: Copy the input TypeScript file to the UUID folder as index.ts
	err = copyFile(filePath, "index.ts")
	if err != nil {
		return "", "", fmt.Errorf("failed to copy file %s to index.ts: %w", filePath, err)
	}

	// Step 8: Set a 30s timeout using context.WithTimeout and execute `ts-node index.ts`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ts-node", "index.ts")
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Start the command
	err = cmd.Start()
	if err != nil {
		return "", "", fmt.Errorf("failed to start ts-node: %w", err)
	}

	// Wait for the command to finish or timeout
	err = cmd.Wait()
	if ctx.Err() == context.DeadlineExceeded {
		return "", "", fmt.Errorf("execution timeout after 30 seconds")
	}
	if err != nil {
		return stdoutBuf.String(), stderrBuf.String(), fmt.Errorf("failed to run ts-node: %w", err)
	}

	// Step 9: Change back to the original directory
	err = os.Chdir(originalDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to change back to original directory: %w", err)
	}

	// Step 10: Delete the UUID folder
	err = os.RemoveAll(filepath.Join("/mnt/persistent", uuidFolder))
	if err != nil {
		log.Printf("Warning: failed to delete folder %s: %s", uuidFolder, err)
	}

	// Return the stdout and stderr of the TypeScript execution
	return stdoutBuf.String(), stderrBuf.String(), nil
}

// copyDirectory copies the contents of srcDir to destDir
func copyDirectory(srcDir string, destDir string) error {
	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(destDir, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, os.ModePerm)
		}

		return copyFile(path, destPath)
	})
	return err
}

// copyFile copies a file from src to dest
func copyFile(src, dest string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	return destFile.Sync()
}

func handleJavaScriptExecution(filePath string) (string, string, error) {
	// Create a context with a 10-second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel() // Ensure resources are cleaned up after the command finishes

	// Create the command using the context
	cmd := exec.CommandContext(ctx, "node", filePath)

	// Get stdout and stderr pipes
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", fmt.Errorf("error while obtaining stdout pipe: %s", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", "", fmt.Errorf("error while obtaining stderr pipe: %s", err)
	}

	// Start the command
	err = cmd.Start()
	if err != nil {
		return "", "", fmt.Errorf("error while starting command: %s", err)
	}

	// Read stdout and stderr
	stdout, err := io.ReadAll(stdoutPipe)
	if err != nil {
		return "", "", fmt.Errorf("error while reading stdout: %s", err)
	}

	stderr, err := io.ReadAll(stderrPipe)
	if err != nil {
		return "", "", fmt.Errorf("error while reading stderr: %s", err)
	}

	// Wait for the command to finish or timeout
	err = cmd.Wait()
	if ctx.Err() == context.DeadlineExceeded {
		return "", "", fmt.Errorf("command timed out after 10 seconds")
	}
	if err != nil {
		return "", "", fmt.Errorf("command execution error: %s", err)
	}

	return string(stdout), string(stderr), nil
}

// Run arbitrary Linux commands, mostly for debugging purposes
func cmdExecHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "Invalid request method"}`, http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error": "Unable to read request body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	cmd := exec.Command("sh", "-c", string(body))

	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()

	err = cmd.Start()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "Failed to start command: %s"}`, err), http.StatusInternalServerError)
		return
	}

	stdout, _ := io.ReadAll(stdoutPipe)
	stderr, _ := io.ReadAll(stderrPipe)
	cmd.Wait()

	response := map[string]string{
		"stdout": string(stdout),
		"stderr": string(stderr),
	}

	if err != nil {
		response["error"] = err.Error()
		w.WriteHeader(http.StatusInternalServerError)
	}

	jsonResponse, _ := json.Marshal(response)
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonResponse)
}

func codeExecHandler(w http.ResponseWriter, r *http.Request) {
	supportedLanguages := []string{"javascript", "typescript"}

	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "Invalid request method"}`, http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error": "Unable to read request body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req CodeExecRequest
	err = json.Unmarshal(body, &req)
	if err != nil {
		http.Error(w, `{"error": "Invalid JSON format"}`, http.StatusBadRequest)
		return
	}

	language := req.Language
	code := req.Code

	fmt.Printf("Language: %s, Code: %s\n", language, code)

	if !isLanguageSupported(language, supportedLanguages) {
		http.Error(w, `{"error": "Language not supported"}`, http.StatusBadRequest)
		return
	}

	randomUUID := uuid.New().String()

	filePath := fmt.Sprintf("/mnt/persistent/%s%s", randomUUID, LANGUAGE_EXTENSIONS[language])

	start := time.Now()

	err = os.WriteFile(filePath, []byte(req.Code), 0644)
	if err != nil {
		errStr := fmt.Sprintf(`{"error": "Unable to write file: %v"}`, err)
		http.Error(w, errStr, http.StatusInternalServerError)
		return
	}

	var stdout, stderr string

	switch language {
	case "javascript":
		stdout, stderr, err = handleJavaScriptExecution(filePath)

	case "typescript":
		stdout, stderr, err = handleTypeScriptExecution(filePath)

	default:
		http.Error(w, `{"error": "Language not supported"}`, http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "Execution error: %s"}`, err), http.StatusInternalServerError)
		return
	}

	err = os.Remove(filePath)
	if err != nil {
		log.Printf("Warning: Unable to delete file %s: %v", filePath, err)
	}

	elapsed := time.Since(start).Milliseconds()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(fmt.Sprintf(`{"stdout": "%s", "stderr": "%s", "execTime": "%d"}`, stdout, stderr, elapsed)))
}

func isLanguageSupported(language string, supportedLanguages []string) bool {
	for _, lang := range supportedLanguages {
		if lang == language {
			return true
		}
	}

	return false
}

func main() {
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/cmdExec", cmdExecHandler)
	http.HandleFunc("/code/exec", codeExecHandler)

	log.Println("Server is starting on port 8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatalf("Server failed: %s", err)
	}
}
