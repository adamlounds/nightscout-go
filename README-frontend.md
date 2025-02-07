# Nightscout-go
Feasibility study to see if Go-based nightscout would be useful.

Based on [nightscout web monitor](https://github.com/nightscout/cgm-remote-monitor)

Process to build/integrate frontend code. Reasonably manual at the moment, but
documenting a proof-of-concept process is the first step.

Assuming a checkout of https://github.com/jameslounds/cgm-remote-monitor in `../cgm-remote-monitor`

```shell
cd ../cgm-remote-monitor/frontend # from https://github.com/jameslounds/cgm-remote-monitor
npm run build # generates a dist folder with static content
cd -

mv ../cgm-remote-monitor/frontend/dist ./
cp -r ../cgm-remote-monitor/frontend/translations ./dist/
```

change to `<script src="https://cdnjs.cloudflare.com/ajax/libs/socket.io/2.3.0/socket.io.js"></script>`
in
```
dist/index.html
dist/admin/index.html
dist/food/index.html
dist/profile/index.html
dist/report/index.html
```
You should now be able to start the server via `go run cmd/server/server.go`
