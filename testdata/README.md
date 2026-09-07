# Test corpus and golden payloads

`corpus/*.eml` are raw messages, exactly as they arrive over the wire (CRLF line
endings, non-UTF-8 bodies stored as raw bytes).

`golden/*.json` are the webhook payloads those messages produced, captured from
the published `oskarspakers/smtp2http:1.0.0` image. They are the reference for
"the payload has not changed", which is what makes the go-smtpsrv removal
reviewable.

To regenerate against a running server on :2525 with its webhook pointed at
`http://host.docker.internal:8099/hook`:

    docker run -d --rm --name s2h-gold -p 2525:2525 oskarspakers/smtp2http:1.0.0 \
      -listen :2525 -webhook http://host.docker.internal:8099/hook
    python3 testdata/capture.py testdata/corpus testdata/golden
    docker stop s2h-gold

## Known-bad output

Five corpus files cover character sets. Three of them are wrong in 1.0.0,
because `decodeCharset` matches a hardcoded, case-sensitive list:

| file | 1.0.0 output |
|---|---|
| `iso88591` | correct |
| `windows1252-exact` | correct |
| `windows1252-lowercase` | mojibake -- name only matched as `Windows-1252` |
| `iso88591-nospace` | mojibake -- lookup requires `; charset=` with a space |
| `iso885915` | mojibake -- charset never handled at all |

Those three goldens are expected to change when the charset lookup is fixed.
That diff is the point, not a regression.
