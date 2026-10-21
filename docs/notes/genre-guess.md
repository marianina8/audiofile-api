# Genre guessing with Bedrock: result

Date: 2026-10-21. Model: `mistral.voxtral-small-24b-2507`, us-west-2, Bedrock Converse API.

Result: **not usable.** All 5 validation calls were rejected before any guess:
`ValidationException: The model returned the following errors: Model does not support audio modality`.
Bedrock's Converse API does not accept audio input for Voxtral (the model card lists "Speech", not "Audio"), so the 0/5 score reflects a rejected request format, not wrong answers.

Decision (Marian): leave genre blank for the 13 tracks whose filename carries no genre. The 5 filename-derived genres stay (Rock, Pop, Industrial x2, Classical). Genres can be set later with `PATCH /tracks/{id}` (`{"genre": "..."}`) or by editing `~/audio/library.csv` before a re-seed.

Not tried: Bedrock's OpenAI-compatible endpoint (which may accept audio for Voxtral) and Gemma 4 E2B (speech-trained). The demo does not depend on genre; "upbeat" uses bpm + energy.
