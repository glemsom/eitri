package constants

// Shared defaults used across Eitri packages.
const (
	PresizeTerminalWidth      = 80
	DefaultRailWidth          = 30
	MinTUIWidth               = 80
	DefaultByteCap            = 64 << 10
	DefaultContextWindow      = 1 << 20
	DefaultCompactionFraction = 0.8
	DefaultTailTurns          = 2
	DefaultKeepRecentTokens   = 8000
	DefaultSummaryMaxTokens   = 4096
	LiveContextWarnThreshold  = 150000
)
