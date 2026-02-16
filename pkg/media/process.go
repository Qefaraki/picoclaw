package media

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxImageSize = 15 * 1024 * 1024 // 15MB raw (base64 adds ~33% → ~20MB encoded)
	maxTextSize  = 100 * 1024       // 100KB
)

// imageExts maps file extensions to MIME types for supported image formats.
var imageExts = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// textExts lists extensions treated as readable text files.
var textExts = map[string]bool{
	".txt": true, ".md": true, ".py": true, ".go": true,
	".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".json": true, ".csv": true, ".xml": true, ".html": true,
	".css": true, ".yaml": true, ".yml": true, ".toml": true,
	".sh": true, ".bash": true, ".zsh": true, ".fish": true,
	".rs": true, ".java": true, ".kt": true, ".c": true,
	".h": true, ".cpp": true, ".hpp": true, ".rb": true,
	".php": true, ".swift": true, ".sql": true, ".r": true,
	".lua": true, ".pl": true, ".env": true, ".ini": true,
	".cfg": true, ".conf": true, ".log": true, ".diff": true,
	".patch": true, ".tex": true, ".rst": true,
}

// ProcessFile reads a file from disk and returns a ContentPart.
// Images are base64-encoded; text files have their content included;
// other/binary files get a placeholder description.
func ProcessFile(path string) (*ContentPart, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	fileName := filepath.Base(path)

	// Empty file guard
	if info.Size() == 0 {
		return &ContentPart{
			Type: "text",
			Text: fmt.Sprintf("[Empty file: %s]", fileName),
		}, nil
	}

	// Image files
	if mimeType, ok := imageExts[ext]; ok {
		if info.Size() > maxImageSize {
			return &ContentPart{
				Type: "text",
				Text: fmt.Sprintf("[Image too large: %s, %.1f MB]", fileName, float64(info.Size())/(1024*1024)),
			}, nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read image %s: %w", path, err)
		}
		return &ContentPart{
			Type:      "image",
			MediaType: mimeType,
			Data:      base64.StdEncoding.EncodeToString(data),
			FileName:  fileName,
		}, nil
	}

	// Text files (by extension or MIME type detection)
	if textExts[ext] || isTextMIME(ext) {
		if info.Size() > maxTextSize {
			return &ContentPart{
				Type: "text",
				Text: fmt.Sprintf("[File too large to include: %s, %.1f KB]", fileName, float64(info.Size())/1024),
			}, nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read text %s: %w", path, err)
		}
		return &ContentPart{
			Type:     "text",
			Text:     fmt.Sprintf("--- Content of %s ---\n%s\n--- End of %s ---", fileName, string(data), fileName),
			FileName: fileName,
		}, nil
	}

	// No recognized extension — sniff content type from first 512 bytes
	if isLikelyText(path) {
		if info.Size() > maxTextSize {
			return &ContentPart{
				Type: "text",
				Text: fmt.Sprintf("[File too large to include: %s, %.1f KB]", fileName, float64(info.Size())/1024),
			}, nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read text %s: %w", path, err)
		}
		return &ContentPart{
			Type:     "text",
			Text:     fmt.Sprintf("--- Content of %s ---\n%s\n--- End of %s ---", fileName, string(data), fileName),
			FileName: fileName,
		}, nil
	}

	// Unsupported / binary
	return &ContentPart{
		Type: "text",
		Text: fmt.Sprintf("[Unsupported file: %s, %d bytes]", fileName, info.Size()),
	}, nil
}

func isTextMIME(ext string) bool {
	mimeType := mime.TypeByExtension(ext)
	return strings.HasPrefix(mimeType, "text/")
}

// isLikelyText reads the first 512 bytes and uses http.DetectContentType
// to determine if a file is likely text (for files with no recognized extension).
func isLikelyText(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	if n == 0 {
		return false
	}
	ct := http.DetectContentType(buf[:n])
	return strings.HasPrefix(ct, "text/") || ct == "application/json"
}
