/*
 * Chainmaker extensions declared in packaged wasmer.h / wasm.h but absent from
 * stock Wasmer 7.2 libwasmer. Stubs for link compatibility with stock builds.
 */
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <wasmer.h>
#include <wasm.h>

void wasm_extern_vec_destroy(wasm_extern_vec_t *vec) {
    if (vec == NULL) {
        return;
    }
    if (vec->data != NULL) {
        free(vec->data);
        vec->data = NULL;
    }
    vec->size = 0;
}

void wasm_config_set_max_pages(wasm_config_t *config, uint32_t max_pages) {
    (void)config;
    (void)max_pages;
}

bool wasm_memory_reset(wasm_memory_t *memory) {
    (void)memory;
    return false;
}
