// Package characterization fija el comportamiento ACTUAL del monolito legado
// (golden master), rarezas incluidas, y verifica que cada reemplazo lo
// reproduzca. No dice qué es correcto: dice qué hace hoy el sistema.
//
// Regenerar los golden (solo desde el legado):
//
//	go test ./characterization -run TestGoldenLegacy -update
package characterization
