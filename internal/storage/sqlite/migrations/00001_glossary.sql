-- +goose Up
create table if not exists glossary_terms (
    id integer primary key autoincrement,
    scope text not null check (scope in ('media', 'common')),
    media_key text,
    normalized_term text not null,
    display_term text not null,
    created_at datetime not null default current_timestamp,
    updated_at datetime not null default current_timestamp,
    last_seen_at datetime not null default current_timestamp
);

create unique index if not exists idx_glossary_terms_media_unique
    on glossary_terms(scope, media_key, normalized_term)
    where scope = 'media';

create unique index if not exists idx_glossary_terms_common_unique
    on glossary_terms(scope, normalized_term)
    where scope = 'common';

create table if not exists glossary_variants (
    id integer primary key autoincrement,
    term_id integer not null references glossary_terms(id) on delete cascade,
    target_language text not null,
    target_text text not null,
    definition text not null default '',
    translation_mode text not null default 'translate',
    category text not null default 'phrase',
    status text not null check (status in ('active', 'suppressed', 'candidate')),
    source text not null check (source in ('generated', 'promoted', 'curated')),
    confidence real not null,
    evidence_count integer not null default 0,
    created_at datetime not null default current_timestamp,
    updated_at datetime not null default current_timestamp,
    last_seen_at datetime not null default current_timestamp
);

create index if not exists idx_glossary_variants_term_language_status
    on glossary_variants(term_id, target_language, status, confidence);

create index if not exists idx_glossary_variants_language_status_source
    on glossary_variants(target_language, status, source);

create table if not exists glossary_observations (
    id integer primary key autoincrement,
    variant_id integer not null references glossary_variants(id) on delete cascade,
    job_id text not null,
    media_key text not null,
    subtitle_path_hash text not null,
    season integer not null default 0,
    episode integer not null default 0,
    snippet text not null default '',
    confidence real not null,
    created_at datetime not null default current_timestamp
);

create index if not exists idx_glossary_observations_variant_created
    on glossary_observations(variant_id, created_at);

create index if not exists idx_glossary_observations_job
    on glossary_observations(job_id);

create index if not exists idx_glossary_observations_media_subtitle
    on glossary_observations(media_key, subtitle_path_hash);

create table if not exists glossary_jobs (
    job_id text primary key,
    media_key text not null,
    subtitle_path_hash text not null,
    status text not null,
    error text not null default '',
    created_at datetime not null default current_timestamp,
    completed_at datetime
);

-- +goose Down
drop table if exists glossary_jobs;
drop table if exists glossary_observations;
drop table if exists glossary_variants;
drop table if exists glossary_terms;
