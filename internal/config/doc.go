// Package config manages the Eitri configuration file (~/.eitri/config.json).
//
// It owns loading, validation, atomic saving, merging partial updates from
// the Settings form, and provider credential normalization.
//
// Key types:
//   - Config — top-level config struct with all settings
//
// Key functions:
//   - Load / Save — read/write config file with atomic write
//   - Validate — check field-level constraints
//   - ValidateSelectedModel — verify model is in live-discovered models
//   - Merge — apply a partial patch from Settings form onto a Config
//   - Defaults — return a Config with sensible defaults
//   - MaskAPIKey — obfuscate API key for logging
//
// Dependencies:
//   - internal/provider — provider profile descriptions and credential validation
//
// Extension points:
//   - Add a new Config field + validation in Validate()
//   - Add a new Merge handler in Merge() for Settings form fields
//   - Adjust defaults in Defaults() when provider profiles or limits change
package config
