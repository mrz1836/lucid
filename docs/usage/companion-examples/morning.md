# Morning template — neutral example

A neutral example of a **morning-window** template. It is opaque prompt text:
Lucid reads this file verbatim and passes it to the model as the user message,
joined with the deterministic context block (the status-panel summary, the recent
observation digest, and your morning routine). Replace it with your own — nothing
here is required wording. Point `companion.morning_template` in `lucid.json` at
your own copy of a file like this.

---

Fill the two slots for a morning message that sets up my day.

- **Interpretation:** read the day honestly from the context below — the chain
  status, the recent body-and-state signals, any change or withdrawal load, my
  commitments, and my intended morning routine. Name what matters this morning,
  what changed, and anything worth watching. Keep it to a few calm sentences.
- **Actions:** give one or two small, concrete things to do next, grounded in the
  routine and the day's signals.

Return only the two slots in the shape the system prompt defines. No header, no
numbers restated, no sign-off.
