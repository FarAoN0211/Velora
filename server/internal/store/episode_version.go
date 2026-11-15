package store

// Episodes have one playback version. Confirmed Castilian audio beats an
// ambiguous Spanish tag, and Latin-only Spanish comes last. This is evaluated
// from current stream metadata on every read, so a later scan cannot restore
// a superseded dub. Movie quality versions and administrative file access stay
// independent. Missing files are sorted before this rank by the caller.
const episodeAudioRank = `coalesce((select max(case
 when lower(coalesce(a.language,'')) in ('es-es','spa-es')
   or lower(coalesce(a.title,'')) ~ '(castellano|castilian|españa|spain)' then 3
 when lower(coalesce(a.title,'')) ~ '(latin|latam|mexic)'
   or lower(coalesce(a.language,'')) ~ '^(es|spa)-(419|mx|ar|cl|co|pe|ve|us)$' then -1
 when lower(coalesce(a.language,'')) in ('es','spa','esp') then 2
 else 0 end)
 from media_streams a where a.file_id=f.id and a.kind='audio'
 and lower(coalesce(a.title,'')) !~ '(commentary|comentario|audiodescri)'),0)`
