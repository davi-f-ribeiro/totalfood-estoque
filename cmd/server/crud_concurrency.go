package main

import "net/http"

const concurrentEditMessage = "Registro alterado por outro operador. Recarregue e revise os dados antes de salvar."

func writeConcurrentEditError(w http.ResponseWriter) {
	writeJSONError(w, http.StatusConflict, concurrentEditMessage)
}
