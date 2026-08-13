# Typed decoders only for byte-verified registers

Prior art (InfinitESP's table registry, Infinitude, acd/infinitive) documents many
more registers than infinid decodes, and contributors will reasonably ask why we
don't just ship them all. We decided a register earns a typed decoder only after
its byte layout is verified against ground truth on a real system (the dated
verification sections of `docs/protocol-tables.md`); everything else takes the
archive-unknown path — counted, preserved, never an error.

Why: register semantics drift across firmware generations — verification on our
Touch-era system repeatedly contradicted documented layouts — and a plausible-but-
wrong decode poisons Home Assistant history silently, which is worse than no value
at all. Archive-unknown turns every undecoded register into a contribution surface
(capture it, verify it, submit the layout) instead of a bug surface.

Consequence: infinid on unfamiliar equipment reports fewer fields than the prior
art suggests it could, by design. The fix is verification, not transcription.
