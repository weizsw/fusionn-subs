# Design: Automatic Free Model Selection

## Context

OpenRouter's free model ecosystem is dynamic:

- Models are added/removed frequently
- Quality varies significantly
- Token count doesn't indicate translation quality
- Code-focused models (e.g., `kat-coder-pro:free` with 107B params) perform poorly on translation

Manual tracking is unsustainable. An AI evaluator can continuously select the best free model for English-to-Chinese subtitle translation.

## Goals / Non-Goals

### Goals

- Automatically select best free OpenRouter model daily
- Use Gemini (free) to evaluate models
- Block startup until initial model selected
- Implement robust fallback strategy
- Filter out code-focused models
- Re-evaluate on restart

### Non-Goals

- Supporting multiple simultaneous models
- Evaluating paid models (focus on free tier)
- Per-job model selection (too expensive)
- Training custom model selector
- Caching model selection across restarts

## Decisions

### Decision 1: Gemini as Evaluator

**What**: Use Gemini 3 Flash (gemini-3-flash) to evaluate OpenRouter free models

**Why**:

- Free with generous quotas (5 RPM)
- Latest Flash generation with improved reasoning
- Doesn't consume OpenRouter free tier quota
- Separation of concerns (evaluator vs worker)
- More stable than using OpenRouter's highest-token free model

**Alternatives considered**:

- OpenRouter free model as evaluator: Consumes quota, less reliable
- Hardcoded priority list: Outdated quickly, manual maintenance

### Decision 2: Daily Evaluation at 3 AM

**What**: Run model evaluation daily at 3 AM UTC

**Why**:

- Low traffic time (won't interrupt active translations)
- Daily cadence captures model changes without over-polling
- Predictable behavior for debugging
- Allows time for new models to stabilize

**Implementation**: Use Go's `time.Ticker` with timezone-aware scheduling

### Decision 3: Block Startup Until Evaluation

**What**: Wait for initial model selection before starting worker

**Why**:

- Ensures valid model from first translation
- Simpler error handling (no "no model selected" state)
- Clear startup failure if evaluation fails
- Acceptable delay (<10s typically)

**Fallback**: If evaluation fails at startup, use `fallback_model` from config

### Decision 4: Three-Tier Fallback Strategy

**What**: selected → last-known-good → fallback_model

**Why**:

- **Selected**: Current evaluation result
- **Last-known**: Survives temporary API failures
- **Fallback**: User-configured safety net

**Example**:

```go
func (s *ModelSelector) GetCurrentModel() string {
    if s.selected != "" { return s.selected }
    if s.lastKnown != "" { return s.lastKnown }
    return s.fallbackModel
}
```

### Decision 5: Filter Code-Focused Models

**What**: Exclude models with "code", "coder", "coding", "programmer" in name/description

**Why**:

- Code models optimized for syntax, not natural language
- Poor translation quality despite high parameter counts
- Example: `kat-coder-pro:free` (107B) worse than `gemini-flash` (small) for translation

**Implementation**:

```go
func isCodeModel(model Model) bool {
    lower := strings.ToLower(model.Name + " " + model.Description)
    codeKeywords := []string{"code", "coder", "coding", "programmer", "codex"}
    for _, kw := range codeKeywords {
        if strings.Contains(lower, kw) { return true }
    }
    return false
}
```

### Decision 6: Evaluation Prompt Design

**What**: Provide model list with metadata, ask for single model name output

**Prompt template**:

```
You are evaluating AI models for English to Chinese subtitle translation.

Available free models:
1. google/gemini-3-flash:free
   Context: 1M tokens, Description: Fast multimodal model
2. meta-llama/llama-3.2-3b-instruct:free
   Context: 128K tokens, Description: Efficient instruction following
...

Requirements:
- High quality English to Chinese translation
- Good instruction following (must use specific format)
- Reliable and stable
- NOT code-focused models

Select the BEST model for this task. Output ONLY the model name, nothing else.
```

**Why this works**:

- Clear criteria (quality, format compliance, stability)
- Metadata helps LLM reason (context length matters)
- Single-line output is easy to parse
- Explicit exclusion of code models

## Architecture

```
┌─────────────────────────────────────────────┐
│           Service Startup                    │
│                                              │
│  1. Load config                              │
│  2. Initialize ModelSelector                 │
│  3. [BLOCKS] Initial evaluation              │
│     ├─ Fetch free models (OpenRouter API)   │
│     ├─ Filter code models                    │
│     ├─ Evaluate with Gemini                  │
│     └─ Select model                          │
│  4. Create translator with selected model    │
│  5. Start worker                             │
└─────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────┐
│       Runtime (Background)                   │
│                                              │
│  ┌────────────────────────────┐             │
│  │  Scheduler (runs at 3 AM)  │             │
│  └────────────┬───────────────┘             │
│               │                              │
│               ▼                              │
│  ┌────────────────────────────┐             │
│  │  Fetch & Evaluate Models   │             │
│  └────────────┬───────────────┘             │
│               │                              │
│               ▼                              │
│  ┌────────────────────────────┐             │
│  │  Update translator model   │             │
│  │  (if different from current)│            │
│  └────────────────────────────┘             │
│                                              │
│  Fallback chain:                             │
│   selected → lastKnown → fallbackModel      │
└─────────────────────────────────────────────┘
```

## Component Breakdown

### 1. OpenRouter Client (`internal/client/openrouter/`)

```go
type Client struct {
    baseURL string
    apiKey  string
}

type Model struct {
    ID          string
    Name        string
    ContextLen  int
    Description string
}

func (c *Client) GetFreeModels() ([]Model, error)
```

### 2. Model Evaluator (`internal/service/modelselection/evaluator.go`)

```go
type Evaluator interface {
    SelectBestModel(models []Model) (string, error)
}

type GeminiEvaluator struct {
    apiKey string
    model  string // Default: gemini-3-flash
}

// Uses official Google Gemini Go SDK (github.com/google/generative-ai-go)
// - Type-safe API
// - Proper error handling
// - Automatic retries
// - Future-proof against API changes
```

### 3. Model Selector (`internal/service/modelselection/selector.go`)

```go
type Selector struct {
    openRouterClient *openrouter.Client
    evaluator        Evaluator
    fallbackModel    string
    
    mu           sync.RWMutex
    selected     string
    lastKnown    string
    lastEvalTime time.Time
}

func (s *Selector) Start() error // Initial + schedule
func (s *Selector) GetCurrentModel() string
func (s *Selector) evaluate() error
```

## Risks / Trade-offs

### Risk 1: Evaluator API Failure

**Impact**: Can't select model, service startup blocked

**Mitigation**:

- Fallback to `fallback_model` after 30s timeout
- Log clear error with instructions
- Continue with fallback model instead of crashing

### Risk 2: Poor Model Selection

**Impact**: Selected model performs poorly on translations

**Mitigation**:

- User can disable auto-selection
- Fallback to user's configured model
- Daily re-evaluation can recover
- Manual override possible via config

### Risk 3: OpenRouter API Changes

**Impact**: Model list format changes, parsing breaks

**Mitigation**:

- Defensive parsing with validation
- Fall back on parse errors
- Log detailed errors for debugging

### Risk 4: Evaluation Cost

**Impact**: Gemini API costs (minimal on free tier)

**Mitigation**:

- Daily cadence = ~30 calls/month (well within free tier)
- Prompt is small (<1K tokens)
- Skip evaluation if no new models

## Configuration Schema

```yaml
openrouter:
  api_key: "sk-or-..."
  
  # Auto model selection (opt-in)
  auto_select_model: true
  
  # Evaluator configuration
  evaluator:
    provider: "gemini"  # Currently only gemini supported
    gemini_api_key: ""  # Optional, reuse from gemini section if empty
    model: "gemini-3-flash"  # Latest Flash model (recommended)
    
  # Fallback if all selection fails
  fallback_model: "google/gemini-3-flash:free"

# Can reuse Gemini section for evaluator
gemini:
  api_key: "AIza..."  # Used by evaluator if evaluator.gemini_api_key empty
```

## Decisions (Additional Scope)

### Manual Re-evaluation Trigger

**Decision**: Not supported in initial implementation

**Rationale**: Daily automatic evaluation is sufficient. Adding manual triggers adds complexity (API endpoints, signals) without clear benefit. Users can restart service if immediate re-evaluation needed.

### Model Persistence Across Restarts

**Decision**: In-memory only, re-evaluate on restart

**Rationale**:

- Keeps system stateless and simple
- Ensures fresh evaluation after deployment/config changes
- Startup blocking is acceptable (<30s)
- Avoids stale model selection issues

### Evaluation Metrics/API

**Decision**: Log only, no metrics endpoint

**Rationale**: Logging provides sufficient visibility. Metrics/API adds infrastructure overhead. Can be added later if monitoring needs emerge.

### Custom Evaluation Prompts

**Decision**: Hardcoded prompt in initial version

**Rationale**: Single well-tested prompt is simpler. Custom prompts risk poor selections. Can make configurable in future if needed.

## Evaluation Prompt Template

The evaluation prompt is stored in `internal/service/modelselection/prompts/model_evaluation.tmpl` and embedded at compile time using `//go:embed`.

**Implementation:**

```go
//go:embed prompts/model_evaluation.tmpl
var evaluationPromptTemplate string

func (e *GeminiEvaluator) buildEvaluationPrompt(models []openrouter.Model) (string, error) {
    tmpl, err := template.New("evaluation").Parse(evaluationPromptTemplate)
    // ... execute template with model data
}
```

**Template Location:** `internal/service/modelselection/prompts/model_evaluation.tmpl`

**Why This Approach:**

- **Separation of concerns**: Prompt content separate from code logic
- **Easy to modify**: Non-developers can tune prompts without touching Go code
- **Type-safe**: Go `text/template` with compile-time embedding via `//go:embed`
- **No runtime dependencies**: Template embedded in binary, no external files needed
- **Idiomatic Go**: Standard library patterns, no third-party template engines

**Prompt Design Rationale:**

- Lists models with metadata so Gemini can reason about capabilities
- Clear criteria emphasizing translation quality and format compliance
- Explicit output format (single line) for easy parsing
- Notes code models already filtered to avoid confusion
- Prioritizes Chinese language support and context understanding
