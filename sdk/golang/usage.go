package agentuse

// Usage counts total input (including cached portions) and output tokens.
// Nil usage means that the harness did not provide usage information.
type Usage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

// RunUsage preserves the measurements actually reported for one run. Missing
// counters remain null; cache read/write are included in total input tokens.
type RunUsage struct {
	InputTokens      *int64 `json:"input_tokens" nullable:"true" minimum:"0" doc:"Input tokens including cached portions; null if not reported."`
	OutputTokens     *int64 `json:"output_tokens" nullable:"true" minimum:"0" doc:"Output tokens; null if not reported."`
	CacheReadTokens  *int64 `json:"cache_read_tokens" nullable:"true" minimum:"0" doc:"Cache-read input tokens; null if not reported."`
	CacheWriteTokens *int64 `json:"cache_write_tokens" nullable:"true" minimum:"0" doc:"Cache-write input tokens; null if not reported."`
}
type Sender struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
