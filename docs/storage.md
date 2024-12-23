Storage config is configured in the STORAGE environment variable.

A pretty-printed example is here, you will probably want to convert to a single line when
declaring your environment though.

```json
{
  "type": "S3",
  "config": {
    "bucket": "nightscout-go",
    "endpoint": "https://e41177ee2064167ccaa0a7b5ea3c7186.r2.cloudflarestorage.com/nightscout-go-dev",
    "region": "",
    "disable_dualstack": false,
    "aws_sdk_auth": false,
    "access_key": "",
    "insecure": false,
    "signature_version2": false,
    "secret_key": "",
    "session_token": "",
    "put_user_metadata": {},
    "http_config": {
      "idle_conn_timeout": "1m30s",
      "response_header_timeout": "2m",
      "insecure_skip_verify": false,
      "tls_handshake_timeout": "10s",
      "expect_continue_timeout": "1s",
      "max_idle_conns": 100,
      "max_idle_conns_per_host": 100,
      "max_conns_per_host": 0,
      "tls_config": {
        "ca_file": "",
        "cert_file": "",
        "key_file": "",
        "server_name": "",
        "insecure_skip_verify": false
      },
      "disable_compression": false
    },
    "trace": {
      "enable": false
    },
    "list_objects_version": "",
    "bucket_lookup_type": "auto",
    "send_content_md5": true,
    "disable_multipart": false,
    "part_size": 67108864,
    "sse_config": {
      "type": "",
      "kms_key_id": "",
      "kms_encryption_context": {},
      "encryption_key": ""
    },
    "sts_endpoint": ""
  },
  "prefix": ""
}
```

At minimum, you will need to provide a value for the `bucket`, `endpoint`,
`access_key` and `secret_key` keys. The rest of the keys are optional.

```json
{"type":"S3","config":{"bucket":"nightscout-go","endpoint":"https://e...6.r2.cloudflarestorage.com/","access_key":"...","secret_key":"...","send_content_md5": false}}
```

Object storage is designed with the following requirements in mind:
- New entries normally result in a single write
- Future entries are not supported and have undefined behaviour
  - (for now, write them to "today" file)
- Startup should not have to load a large number of files
- Unexpected files should not cause issues
  - We should not have to enumerate/iterate files in storage
- Loading files should not require de-duping
  - We can receive entries out-of-order, but always store them in-order
  - The files fetched at boot should not contain overlapping periods
- We can leverage the object store provider's rules to out old objects

With that in mind, we have the following storage types:

#### Archived year-files
 - ...have no retention rules
 - ...contain a year's worth of events
 - ...should never need updating

#### Current year-file
  - no retention rule
  - Contain data for "completed" months
  - Typically updated monthly at the start of a new month

#### Current month-file
  - 18-month retention (Can rebuild current year, plus 6-month leeway)
  - Contain data for "completed" days within this month
  - Typically updated daily at the start of a new day

#### Current day-file
  - 7-month retention (can rebuild current month, plus 6-month leeway)
  - Contain data for the current day
  - Typically updated when new events are received

### Write algorithm

When new events are received:

1. If all events are within the current day (technically, after 00:00 this
   morning - should include future entries), update the current-day file
2. If we pass into a new day, write a completed, backup version of the previous
   day. Write a new version of the current-month file containing all entries
   this month, except for events occurring today.
3. If we pass into a new month, write a completed, backup version of the
   previous month. Write a new version of the current-year file containing all
   entries this year, except for events occurring this month.
4. If we pass into a new year, write a completed, backup version of the
   previous year.

Note: "current" day/month/year is based on system time, not entry time.

Rationale for writing completed backup files: the backup files should have
a degree of usefulness, otherwise there's no point in keeping them.
Most of the time, the previous-day backup will be unchanged, as entries tend to
come in one-by-one from a sensor (so 2024-11-01.json will _already_ contain
everything from that day, the next reading will go into 2024-11-02.json).
However, without consideration, the month file would never contain entries from
the last day of the month.
For example on 2024-11-30, the month file contains 2024-11-01 to 2024-11-29, but
on the next day we'd be using 2024-12.json as the current-month file.
Similarly, the year files would never contain entries for December.

Caveat: Backup files are not considered canonical.
Bulk imports are not expected to populate all the relevant backup files.

When historical events are received
1. if event occurred today, set the "current-day" dirty flag.
2. otherwise, if event occurred earlier this month, set the "current-month"
   dirty flag.
3. otherwise, if event occurred earlier this year, set the "current-year"
   dirty flag
4. otherwise, set a dirty flag for the appropriate year. (nb: If a
   previous-year dirty flag is set, may need to load historical data for that
   year and merge with the in-memory working set)
5. Re-write any dirty files

Re-write the appropriate files.

### Read algorithm (boot)

  - load last year's completed year-file (so we have some history on jan 1st)
  - load the current year-file
  - load the current month-file.
  - load the current day-file.
