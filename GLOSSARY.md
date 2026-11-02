# sfdb

A standalone CLI that clones selected Salesforce objects into a SQLite database for the IDE's Database tool.

## Language

**Object selector**:
A CLI flag that resolves to Salesforce object API names: `--all-objects`, `--custom-objects`, or `--standard-objects`.
_Avoid_: filter, query

**Object set**:
The deduplicated, alphabetically sorted API names produced by combining `--objects` with any object selectors.
_Avoid_: object list, allowlist

**Standard object**:
A Salesforce object whose Describe Global `custom` flag is false. Standard objects include most system objects.
_Avoid_: system object, built-in object

**Custom object**:
A Salesforce object whose Describe Global `custom` flag is true, including managed-package objects and custom settings.
_Avoid_: user object

**System object**:
A derived object whose API name ends in `History`, `Share`, `Feed`, `Tag`, or `ChangeEvent`.
_Avoid_: audit object, internal object
