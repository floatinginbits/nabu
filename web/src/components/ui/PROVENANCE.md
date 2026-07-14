# shadcn/ui component provenance

Vendored component source is invisible to npm audit/Dependabot
(ADR-0002 risk 2, `security-baseline.md`): record every component pulled here,
and re-pull when an upstream security or accessibility fix lands.
Advisories to watch: https://github.com/shadcn-ui/ui and Radix releases.

Config: base `radix`, style `radix-vega`, CSS variables, base color neutral
(see `components.json`).

| Component | Pulled with (shadcn CLI) | Date       |
| --------- | ------------------------ | ---------- |
| button    | 4.13.0                   | 2026-07-15 |
| input     | 4.13.0                   | 2026-07-15 |
| card      | 4.13.0                   | 2026-07-15 |
