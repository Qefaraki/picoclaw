package media

// ContentPart represents a single part of a multimodal message.
// Used to pass processed media between channels, bus, and providers
// without circular imports.
type ContentPart struct {
	Type      string `json:"type"`       // "text" or "image"
	Text      string `json:"text"`       // for type="text"
	MediaType string `json:"media_type"` // MIME type, e.g. "image/jpeg"
	Data      string `json:"data"`       // base64-encoded image data
	FileName  string `json:"file_name"`  // original filename
}
