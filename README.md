# Nightscout-go
Feasibility study to see if Go-based nightscout would be useful.

## Aims
- can we lose the mongo dependency?
- Does it use less RAM/CPU?
- Can we use pluggable storage via adapter repositories?
    - Could we use s3/dynamo, cloudflare r2/kv or other free-tier storage?

## Initial Spike
 - [X] support uploads from [nightscout-librelink-up](https://github.com/timoschlueter/nightscout-librelink-up)
 - [X] support [MacOS menu bar](https://github.com/adamd9/Nightscout-MacOS-Menu-Bar) (nb: only supports https)
 - [X] unauthenticated api calls should fail, ie support `AUTH_DEFAULT_ROLES=denied`

## Usefully deployable

- [X] Use in-memory data store. Remove postgres.
      Do not fix >11k entry pg import issue (caused by too-long sql statement)
   - [X] add to memory store when new entries received
     - [X] Write current day-, month- and year- files as appropriate
   - [X] read from memory store when returning current entry
   - [X] use memory store if possible for `entries` (ie >count entries in memory)
 - [X] Persist to s3 on shutdown (not needed, s3 is always up-to-date with latest data)
 - [X] Read from s3 on startup
 - [X] Trigger write to s3 on each receipt of new data
 - [X] Support larger bulk-insert. Currently limited to 10,802 entries without batch pg inserts
 - [ ] Ignore duplicate data (same reading, same 30s period -> make nightscoutjs import work)
   - [X] Can restart server with librelinkup enabled and we do not get duplicate entries
 - [ ] Write completed "backup" files when passing into new month/year
 - [X] Support single-shot import from remote nightscout

## Enough to be self-contained useful #1: Nightscout menu bar works

 - [X] Fetch data from librelinkup every minute = Nightscout menu bar works
 - [ ] Use generated tokens, do not hardcode
 - [ ] hardcoded "api:read:entries" token name (derived from API_SECRET) "read-xxx"

## Basic shuggah support
Note that shuggah supports token authentication by sending it in the api-secret header
 - [X] endpoint `GET /api/v1/experiments/test`
 - [X] endpoint `GET /api/v1/entries/sgv.json`
 - [X] endpoint `GET /api/v1/treatments?find[created_at][$gt]=<a day ago>` (can return [] for now)
 - [X] endpoint `POST /api/v1/treatments`
 - [X] endpoint `GET /api/v1/treatments` should return treatments
 - [X] store treatments in s3, load on boot
 - [X] endpoint `DELETE /api/v1/treatments/<_id>` to delete treatments
 - [X] endpoint `PUT /api/v1/treatments/<_id>` to update treatments

## Basic Nightguard support
 - [ ] support `GET /api/v1/treatments?count=1&find[eventType]=Site+Change` etc
 - [ ] support `/api/v2/properties`
 - [ ] support date range (gt/lte) on `GET /api/v1/entries.json`


##  Next Steps
 - [ ] serve bundled front-end
 - [ ] implement socket interface for f/e. See https://github.com/socketio/engine.io-protocol/tree/v3

## Discoveries:
- v1 api has at least four different ways to authenticate
- various api endpoints essentially expose mongodb queries directly - mapping
  these to another data store will require thought
- various endpoints are not documented (eg `/api/v1/entries/current`)
- not sure how widely-used the permissions model is - for example, the
  `devicestatus-upload` permission does not allow `nightscout-librelink-up` to
  work as it fetches from `/api/v1/entries?count=1` to determine the most-recent
  sgv before uploading new ones
- nightscout will return the most-recent-before-current-time entry for the
  `current` apiv1 endpoint, not the max-time entry
- the v1 api `entries` endpoint will add an implicit date filter
  (of now - 4 days) if one is specified in the query.
  (see `lib/server/query.js` `TWO_DAYS * 2`).
  Use `find[date][$gte]=1` to bypass.


### Known-used Endpoints
[MacOS menu bar](https://github.com/adamd9/Nightscout-MacOS-Menu-Bar)
- `/api/v1/entries?count=60&token=<tok>`

No other endpoints are used

[nightscout-librelink-up](https://github.com/timoschlueter/nightscout-librelink-up)
- `GET /api/v1/entries?count=1` (determine most-recent known entry before each upload)
- `POST /api/v1/entries` (upload any new sgv entries)
Perms to support this would be `api:entries:read,create`

[Scoutnight](http://scoutnight.netlify.app)
- `/api/v1/entries.json?find[type]=sgv&find[date][$gte]=1722985200000&count=26000&token=<tok>`
- `/api/v1/treatments.json?count=10&find[eventType]=/Sensor%20Start|Sensor%20Change/&find[created_at][$gte]=2024-10-05T10:31:12.962Z&token=<tok>`
- `/api/v1/treatments.json?find[created_at][$gte]=2024-11-05T00:00:00.000Z&find[created_at][$lte]=2024-11-05T23:59:59.999Z&find[$or][0][insulin][$exists]=true&find[$or][1][carbs][$exists]=true&token=<tok>&count=100`
- `/api/v1/entries.json?find[date][$gte]=1730764800000&find[date][$lt]=1730851200000&count=100000&token=<tok>`
- `/api/v1/entries.json?find[date][$gte]=1730545986805}&count=900&token=<tok>`
- `/api/v1/entries.json?find[date][$gt]=1730805552000}&count=100&timestamp=1730805796869&token=<tok>`  ( note stray `}` 🙄)
- `/api/v1/entries.json?find[type]=sgv&find[date][$gte]=1722985200000&count=26000&token=ffs`
- `/api/v1/devicestatus?count=1&token=<tok>`

Note: sometimes datetime has trailing `}`. Both `$gt` and `$gte` are used.
Regexp used to find Sensor Start/Change entries

nightguard ios app fetches much more info :)

```
GET /api/v1/devicestatus.json?count=5&token=xxx-123
GET /api/v1/entries.json?count=1440&find%5Bdate%5D%5B$gt%5D=1733875200000.0&find%5Bdate%5D%5B$lte%5D=1733961600000.0&token=xxx-123
GET /api/v1/entries.json?count=1440&find%5Bdate%5D%5B$gt%5D=1734027035000.0&find%5Bdate%5D%5B$lte%5D=1734048000000.0&token=xxx-123
GET /api/v1/entries.json?find%5Bdate%5D%5B$gt%5D=1734026975000.0&find%5Bdate%5D%5B$lte%5D=1734048000000.0&count=1440&token=xxx-123
GET /api/v1/treatments?count=1&find%5Bcreated_at%5D%5B$gte%5D=2024-11-28&find%5BeventType%5D=Sensor%20Change&token=xxx-123
GET /api/v1/treatments?count=1&find%5Bcreated_at%5D%5B$gte%5D=2024-11-28&find%5BeventType%5D=Sensor%20Change&token=xxx-123
GET /api/v1/treatments?count=1&find%5BeventType%5D=Pump%20Battery%20Change&find%5Bcreated_at%5D%5B$gte%5D=2024-11-02&token=xxx-123
GET /api/v1/treatments?count=1&find%5BeventType%5D=Site%20Change&find%5Bcreated_at%5D%5B$gte%5D=2024-12-07&token=xxx-123
GET /api/v1/treatments?count=1&find%5BeventType%5D=Temporary%20Target&find%5Bcreated_at%5D%5B$gte%5D=2024-12-11&token=xxx-123
GET /api/v1/treatments?find%5Bcreated_at%5D%5B$gte%5D=2024-12-07&count=1&find%5BeventType%5D=Site%20Change&token=xxx-123
GET /api/v1/treatments?find%5BeventType%5D=Pump%20Battery%20Change&count=1&find%5Bcreated_at%5D%5B$gte%5D=2024-11-02&token=xxx-123
GET /api/v1/treatments?find%5BeventType%5D=Pump%20Battery%20Change&find%5Bcreated_at%5D%5B$gte%5D=2024-11-02&count=1&token=xxx-123
GET /api/v1/treatments?find%5BeventType%5D=Sensor%20Change&find%5Bcreated_at%5D%5B$gte%5D=2024-11-28&count=1&token=xxx-123
GET /api/v1/treatments?find%5BeventType%5D=Site%20Change&count=1&find%5Bcreated_at%5D%5B$gte%5D=2024-12-07&token=xxx-123
GET /api/v1/treatments?find%5BeventType%5D=Temporary%20Target&count=1&find%5Bcreated_at%5D%5B$gte%5D=2024-12-11&token=xxx-123
GET /api/v1/treatments.json?token=xxx-123
GET /api/v2/properties?token=xxx-123
```

[Nightscout pro data transfer tool](https://github.com/AndyLow91/nightscout-data-transfer)
```
GET /api/v1/entries.json?count=all&find[dateString][$lte]=2024-11-20&find[dateString][$gte]=2024-07-01
GET /api/v1/treatments.json?count=all&find[created_at][$lte]=2024-11-20&find[created_at][$gte]=2024-07-01
POST /api/v1/entries
POST /api/v1/treatments
```

[Juggluco](https://www.juggluco.nl/Juggluco/webserver.html) says it implements a nightscout-compatible server.
```
GET /api/v1/entries/sgv.json
GET /api/v1/entries.json
GET /api/v1/entries/sgv.csv
GET /api/v1/entries/sgv.tsv or http://127.0.0.1:17580/api/v1/entries/sgv.txt or http://127.0.0.1:17580/api/v1/entries
GET /api/v1/entries/current
GET /api/v1/treatments

supports count and find:
find[date][$gt]=datemsec
find[date][$gte]=datemsec
find[date][$lt]=datemsec
find[date][$lte]=datemsec
/api/v1/entries.json?count=5&find[dateString][$lt]=2023-03-02T08:04:01
```
