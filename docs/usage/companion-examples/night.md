# Night template — neutral example

A neutral example of a **night-window** template. It is opaque prompt text: Lucid
reads this file verbatim and passes it to the model as the user message, joined
with the deterministic context block (the status-panel summary, the recent
observation digest, and your night routine). Replace it with your own — nothing
here is required wording. Point `companion.night_template` in `lucid.json` at your
own copy of a file like this.

---

Fill the two slots for a night message that closes out my day.

- **Interpretation:** read the day back from the context below — how the chain
  stands, the body-and-state signals logged today, any change or withdrawal load,
  and my night routine. Close the loop honestly: name what the day was, what
  landed, and what to carry into tomorrow. If the day was missed, do not soften it
  — Lucid appends the Engine's own verdict line below your message, and you must
  not restate, preview, or reword it.
- **Actions:** give a single grounded close-out step — the one thing worth doing
  before sleep, from my night routine.

Return only the two slots in the shape the system prompt defines. No header, no
numbers restated, no sign-off.
