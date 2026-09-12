# Regression tests — sample-session

Drafted from confirmed findings in session `sample-session` (app `testimony demo`, participant `P1`). 2 of 3 drafts accepted.

## T-001 — Saving gives no confirmation

- **Source:** finding `F-001` (bug, severity 3) in session `sample-session`, at [00:22]
- **Decision:** accepted (2026-09-12)

**Steps**

1. Open #general in the settings prototype.
2. Change the display name to Alice.
3. Click the Save button \(\[data-testid=save-btn\]\).

**Expected:** The save is confirmed on screen — a toast, or the button briefly disabled.

**Observed:** Nothing visibly changes, so there is no way to tell the save landed.

**Rationale (participant, [00:22]):** “I clicked save and nothing happened”

## T-002 — Saving a display name twice gives no confirmation either time

- **Source:** finding `F-001` (bug, severity 3) in session `sample-session`, at [00:22]
- **Decision:** edited (2026-09-12)

**Steps**

1. Open #general in the settings prototype.
2. Change the display name to Alice.
3. Click the Save button \(\[data-testid=save-btn\]\).
4. Click the Save button a second time.

**Expected:** The second click is either confirmed or visibly a no-op.

**Observed:** Both clicks look the same, so the only way to check is to click again.

**Rationale (participant, [00:22]):** “I clicked save and nothing happened”
