// host/capture_sink.go — saves the image bytes and CRC32 that `jag run` would PUT.
package main

import ("fmt"; "io"; "net/http"; "os")

func main() {
	h := func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			fmt.Printf("ERROR reading body on %s %s: %v\n", r.Method, r.URL.Path, err)
			http.Error(w, err.Error(), 500)
			return
		}
		if err := os.WriteFile("image", body, 0644); err != nil {
			fmt.Printf("ERROR writing image on %s %s: %v\n", r.Method, r.URL.Path, err)
			http.Error(w, err.Error(), 500)
			return
		}
		os.WriteFile("image.crc32", []byte(r.Header.Get("X-Jaguar-CRC32")), 0644)
		fmt.Printf("captured %d bytes, crc32=%s on %s %s\n",
			len(body), r.Header.Get("X-Jaguar-CRC32"), r.Method, r.URL.Path)
		w.WriteHeader(200)
	}
	http.HandleFunc("/run", h)
	http.HandleFunc("/install", h)
	fmt.Println("capture sink on :8080")
	http.ListenAndServe(":8080", nil)
}
