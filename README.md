# Nightscout-go
Feasibility study to see if Go-based nightscout would be useful.

## Aims
- can we lose the mongo dependency?
- Does it use less RAM/CPU?
- Can we use pluggable storage via adapter repositories?
    - Could we use s3/dynamo, cloudflare r2/kv or other free-tier storage?

## Initial Spike
 - support uploads from [nightscout-librelink-up](https://github.com/timoschlueter/nightscout-librelink-up)
 - support [MacOS menu bar](https://github.com/adamd9/Nightscout-MacOS-Menu-Bar)
 - unauthenticated api calls should fail, ie support `AUTH_DEFAULT_ROLES=denied`

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
- `GET /api/v1/entries?count=1` (determine most-recent known event before each upload)
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
Regexp used to find Sensor Start/Change events

