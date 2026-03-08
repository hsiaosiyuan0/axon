# Configuration

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `AXON_DB` | `~/.axon/axon.db` | Database path |
| `AXON_DEFAULT_MODEL` | `purego` | Embedding model |
| `AXON_LLM_ENDPOINT` | `https://api.openai.com/v1` | LLM API endpoint |
| `AXON_LLM_API_KEY` | _(none)_ | LLM API key |
| `AXON_LLM_MODEL` | `gpt-4o-mini` | LLM model name |
| `AXON_API_KEY` | _(none)_ | HTTP API secret key |

All LLM features are optional — Axon works fully offline without any API key configured.

---

## Embedding models

| Model | Type | Notes |
|-------|------|-------|
| `purego` | Built-in TF-IDF | Default, no API needed |
| `api:text-embedding-3-small` | OpenAI API | Best quality, needs key |
| `api:text-embedding-ada-002` | OpenAI API | Good quality |

```bash
axon model list          # list available models
axon re-embed -m <model> # re-embed knowledge base with a new model
```

---

## Using local / alternative LLMs

```bash
# Ollama
AXON_LLM_ENDPOINT=http://localhost:11434/v1 \
AXON_LLM_MODEL=llama3.2 \
axon relate --llm

# OpenRouter
AXON_LLM_ENDPOINT=https://openrouter.ai/api/v1 \
AXON_LLM_API_KEY=sk-or-... \
axon relate --llm
```
