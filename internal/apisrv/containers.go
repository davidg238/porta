package apisrv

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/control"
)

// maxUpload caps an uploaded image, enforced via http.MaxBytesReader (the real
// size limit; ParseMultipartForm's arg is only the in-memory threshold).
const maxUpload = 8 << 20

// handleContainerInstall accepts a multipart .bin upload, registers it as the
// payload, and enqueues a run via control.Install.
func (h *Handler) handleContainerInstall(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	if err := h.st.EnsureNode(id, h.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	file, hdr, err := r.FormFile("image")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "image file required")
		return
	}
	defer file.Close()

	name := r.FormValue("name")
	if name == "" {
		name = strings.TrimSuffix(hdr.Filename, ".bin")
	}
	if name == "" {
		writeErr(w, http.StatusBadRequest, "container name required")
		return
	}

	opts := control.InstallOpts{Lifecycle: r.FormValue("lifecycle"), Runlevel: 3}
	if rl := r.FormValue("runlevel"); rl != "" {
		n, err := strconv.Atoi(rl)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runlevel must be an integer")
			return
		}
		opts.Runlevel = n
	}
	if iv := r.FormValue("interval"); iv != "" {
		secs, err := command.ParseDurationSeconds(iv)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		opts.IntervalS = secs
	}
	opts.Triggers = r.MultipartForm.Value["trigger"] // repeatable field

	cmdID, err := control.Install(h.st, id, name, file, opts, "api", h.now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w, map[string]any{"command_id": cmdID, "size": hdr.Size})
}
