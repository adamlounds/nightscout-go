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
 - [ ] Use in-memory data store
   - [ ] add to memory store when new entries received
   - [ ] read from memory store when returning current entry
   - [ ] use memory store if possible for `entries` (ie >count entries in memory)
 - [ ] Persist to s3 on shutdown
 - [ ] Read from s3 on startup
 - [ ] Persist to s3 every 15m
 - [ ] Ignore duplicate data (same reading, same 30s period -> make nightscoutjs import work)

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
"/api/v1/devicestatus.json?count=5"
"/api/v1/treatments?count=1&find%5BeventType%5D=Temporary%20Target&find%5Bcreated_at%5D%5B$gte%5D=2024-11-05"
"/api/v1/treatments?count=1&find%5BeventType%5D=Pump%20Battery%20Change&find%5Bcreated_at%5D%5B$gte%5D=2024-09-27"
"/api/v1/treatments?count=1&find%5BeventType%5D=Sensor%20Change&find%5Bcreated_at%5D%5B$gte%5D=2024-10-23"
"/api/v1/treatments?count=1&find%5BeventType%5D=Site%20Change&find%5Bcreated_at%5D%5B$gte%5D=2024-11-01"
"/api/v2/properties?"
"/api/v1/treatments.json?"
"/api/v1/entries.json?find%5Bdate%5D%5B$gt%5D=1730851200000.0&find%5Bdate%5D%5B$lte%5D=1730937600000.0&count=1440"
"/api/v1/treatments?count=1&find%5Bcreated_at%5D%5B$gte%5D=2024-11-05&find%5BeventType%5D=Temporary%20Target"
"/api/v1/entries.json?find%5Bdate%5D%5B$gt%5D=1730764800000.0&find%5Bdate%5D%5B$lte%5D=1730851200000.0&count=1440"
"/api/v1/status.json?"
"/api/v1/entries.json?find%5Bdate%5D%5B$lte%5D=1730937600000.0&find%5Bdate%5D%5B$gt%5D=1730932773000.0&count=1440"
"/api/v2/properties?"
"/api/v1/treatments?find%5BeventType%5D=Site%20Change&count=1&find%5Bcreated_at%5D%5B$gte%5D=2024-11-01"
"/api/v1/treatments?count=1&find%5Bcreated_at%5D%5B$gte%5D=2024-10-23&find%5BeventType%5D=Sensor%20Change"
```

[https://github.com/AndyLow91/nightscout-data-transfer](Nightscout pro data transfer tool)
```
GET /api/v1/entries.json?count=all&find[dateString][$lte]=2024-11-20&find[dateString][$gte]=2024-07-01
GET /api/v1/treatments.json?count=all&find[created_at][$lte]=2024-11-20&find[created_at][$gte]=2024-07-01
POST /api/v1/entries
POST /api/v1/treatments
```
