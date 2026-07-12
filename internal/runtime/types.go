package runtime

type WorkerID string
type UpstreamID string
type Generation uint64
type WorkerRole string
type LogLevel string
type APIFormat string
type ProtocolKind string
type ProtocolCapability string

const (
	WorkerRoleCLI WorkerRole = "cli"
	WorkerRoleApp WorkerRole = "app"

	LogLevelSimple LogLevel = "simple"
	LogLevelDetail LogLevel = "detail"

	APIFormatResponses       APIFormat = "responses"
	APIFormatChatCompletions APIFormat = "chat_completions"
	APIFormatAnthropic       APIFormat = "anthropic"

	ProtocolResponses       ProtocolKind = "responses"
	ProtocolChatCompletions ProtocolKind = "chat_completions"
	ProtocolAnthropic       ProtocolKind = "anthropic"

	ProtocolCapabilityInputText    ProtocolCapability = "input_text"
	ProtocolCapabilityToolCalls    ProtocolCapability = "tool_calls"
	ProtocolCapabilityStreamEvents ProtocolCapability = "stream_events"
)

type ModuleConfig struct {
	Enabled bool           `json:"enabled"`
	Params  map[string]any `json:"params,omitempty"`
}

type ModuleProtocolSupport struct {
	Protocols    []ProtocolKind       `json:"protocols,omitempty"`
	Capabilities []ProtocolCapability `json:"capabilities,omitempty"`
}

type PluginRuntime struct {
	Kind            string                `json:"kind"`
	Source          string                `json:"source"`
	Path            string                `json:"path,omitempty"`
	Command         string                `json:"command,omitempty"`
	Args            []string              `json:"args,omitempty"`
	ProtocolVersion string                `json:"protocol_version,omitempty"`
	ProtocolSupport ModuleProtocolSupport `json:"protocol_support,omitempty"`
}

type UpstreamRuntime struct {
	ID        UpstreamID `json:"id"`
	BaseURL   string     `json:"base_url"`
	APIKey    string     `json:"api_key,omitempty"`
	APIFormat APIFormat  `json:"api_format,omitempty"`
}

type UpstreamPublic struct {
	ID        UpstreamID `json:"id"`
	BaseURL   string     `json:"base_url"`
	HasAPIKey bool       `json:"has_api_key"`
	APIFormat APIFormat  `json:"api_format,omitempty"`
}

type StreamTimeouts struct {
	FirstByteMilliseconds int64 `json:"first_byte_ms,omitempty"`
	IdleMilliseconds      int64 `json:"idle_ms,omitempty"`
}

type WorkerRuntime struct {
	ID             WorkerID                 `json:"id"`
	Generation     Generation               `json:"generation"`
	ListenPort     int                      `json:"listen_port"`
	Role           WorkerRole               `json:"role,omitempty"`
	LogLevel       LogLevel                 `json:"log_level,omitempty"`
	ProxyURL       string                   `json:"proxy_url,omitempty"`
	StreamTimeouts StreamTimeouts           `json:"stream_timeouts,omitempty"`
	Upstream       UpstreamRuntime          `json:"upstream"`
	Plugins        map[string]PluginRuntime `json:"plugins,omitempty"`
	Modules        map[string]ModuleConfig  `json:"modules,omitempty"`
	Hooks          map[string]ModuleConfig  `json:"hooks,omitempty"`
}

func (u UpstreamRuntime) Public() UpstreamPublic {
	return UpstreamPublic{
		ID:        u.ID,
		BaseURL:   u.BaseURL,
		HasAPIKey: u.APIKey != "",
		APIFormat: u.APIFormat,
	}
}

func ProtocolKindFromAPIFormat(format APIFormat) ProtocolKind {
	if format == "" {
		return ProtocolResponses
	}
	return ProtocolKind(format)
}
