# Adding A Module

This monorepo keeps module onboarding simple: add the module where it lives, document how to run it, and make sure it has a place for evidence.

## Required Outputs

- module directory in a domain (`workloads/`, `automation/`, or `infra/`)
- module manifest (`module.yaml`) describing the managed component IDs and the health command you expect operators to use
- module runbook overlay under `docs/runbooks/`
- module evidence archive directory under `ops/evidence/`
- any module-specific tests or validation steps added to the right test or deploy workflow

## Steps

1. Scaffold:
```bash
./tools/import/new_module_scaffold.sh <module_id> <domain_dir> <component_a,component_b>
```
2. Fill in the manifest with the real component IDs and health commands.
3. Add or update the module runbook with local checks, deploy steps, and validation steps.
4. Create the evidence directory under `ops/evidence/` if the scaffold did not already cover it.
5. Add tests and validation checks that match the module's stack.
6. Update the root docs when the module changes the operator-facing shape of the repo.

## Acceptance Gates

- module directory and `module.yaml` exist in the right domain
- module runbook explains how to check, deploy, and validate it
- evidence directory exists under `ops/evidence/`
- docs link check passes (`./tools/docs/check_links.sh`)
- evidence validation passes (`./tools/observability/validate_evidence.sh`)
- tests or local validation pass for the module you added
