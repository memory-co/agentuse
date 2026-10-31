package agentuse

// Usage counts total input (including cached portions) and output tokens.
// Nil usage means that the harness did not provide usage information.
type Usage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}
type Sender struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
