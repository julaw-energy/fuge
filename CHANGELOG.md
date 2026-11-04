# Changelog

## v0.1.1
- Dependencies updated to their latest releases, including `github.com/centrifugal/centrifuge` v0.40.1 and `github.com/santhosh-tekuri/jsonschema/v6` v0.3.6.
- The module requires Go 1.27.3.

## v0.1.0
- Own module `github.com/julaw-energy/fuge` (was `github.com/julaw-energy/gas/pkg/fuge`).
- Meta-schema validation is opt-in: removed `ValidateBytes` and `Document.Validate`; use `validate.Bytes` and set `Config.Validator`.
  `API.JSON` no longer validates by default.
- YAML rendering uses `go.yaml.in/yaml/v3` instead of `gopkg.in/yaml.v3`.
