package main

import "net/http"

const concurrentEditMessage = "Registro alterado por outra pessoa. Atualize a página e confira os dados antes de salvar."

func writeConcurrentEditError(w http.ResponseWriter) {
	writeJSONError(w, http.StatusConflict, concurrentEditMessage)
}
