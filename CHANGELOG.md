# Changelog

## v0.1.0
- Own module `github.com/julaw-energy/fuge` (was `github.com/julaw-energy/gas/pkg/fuge`).
- Meta-schema validation is opt-in: removed `ValidateBytes` and `Document.Validate`; use `validate.Bytes` and set `Config.Validator`.
  `API.JSON` no longer validates by default.
- YAML rendering uses `go.yaml.in/yaml/v3` instead of `gopkg.in/yaml.v3`.
