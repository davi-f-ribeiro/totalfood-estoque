package main

// searchAccents são os argumentos de translate() usados nas buscas por nome:
// translate(lower(x), ` + searchAccents + `). Assim "acu" encontra "Açúcar" e
// "cafe" encontra "Café". Era a regra da lista de requisições; passou a valer
// para todas as buscas. O frontend usa a mesma ideia em foldSearch (static/app.js).
const searchAccents = "'áàâãäéèêëíìîïóòôõöúùûüç','aaaaaeeeeiiiiooooouuuuc'"
