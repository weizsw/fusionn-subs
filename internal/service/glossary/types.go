package glossary

import "time"

type Scope string
type VariantStatus string
type VariantSource string
type TranslationMode string
type Category string
type MediaKeySource string

const (
	ScopeMedia  Scope = "media"
	ScopeCommon Scope = "common"

	StatusActive     VariantStatus = "active"
	StatusSuppressed VariantStatus = "suppressed"
	StatusCandidate  VariantStatus = "candidate"

	SourceGenerated VariantSource = "generated"
	SourcePromoted  VariantSource = "promoted"
	SourceCurated   VariantSource = "curated"

	TranslationModeTranslate     TranslationMode = "translate"
	TranslationModePreserve      TranslationMode = "preserve"
	TranslationModeTransliterate TranslationMode = "transliterate"
	TranslationModeContextual    TranslationMode = "contextual"

	CategoryAcronym       Category = "acronym"
	CategoryOrganization  Category = "organization"
	CategoryCharacter     Category = "character"
	CategoryPlace         Category = "place"
	CategoryTechnicalTerm Category = "technical_term"
	CategoryPhrase        Category = "phrase"

	MediaKeySourceExternalID MediaKeySource = "external_id"
	MediaKeySourceMediaID    MediaKeySource = "media_id"
	MediaKeySourceTitle      MediaKeySource = "title"
	MediaKeySourcePath       MediaKeySource = "path"
)

type MediaKey struct {
	Value  string
	Source MediaKeySource
}

type PromptEntry struct {
	Scope           Scope
	MediaKey        string
	NormalizedTerm  string
	DisplayTerm     string
	TargetLanguage  string
	TargetText      string
	Definition      string
	TranslationMode TranslationMode
	Category        Category
	Status          VariantStatus
	Source          VariantSource
	Confidence      float64
	EvidenceCount   int
	LastSeenAt      time.Time
}

type PromptOptions struct {
	MediaKey            string
	InjectMinConfidence float64
	MaxPromptEntries    int
}

type Candidate struct {
	Term           string
	NormalizedTerm string
	Frequency      int
	Snippets       []string
}
