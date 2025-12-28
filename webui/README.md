# Web UI

`index.html` is a lightweight dashboard that shows daemon status and the services list via the REST API. The Go `webui` package embeds everything in this folder so the daemon can serve it directly from memory. Replace/extend this static page with your preferred frontend build (Vite/React/etc.) and keep the compiled output in this directory. 
