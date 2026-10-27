package wasmer

// #include <wasmer.h>
import "C"
import "runtime"

// Store represents all global state that can be manipulated by
// WebAssembly programs. It consists of the runtime representation of
// all instances of functions, tables, memories, and globals that have
// been allocated during the life time of the abstract machine.
//
// The Store holds the Engine (that is — amongst many things — used to
// compile the Wasm bytes into a valid module artifact).
//
// # See also
//
// Specification: https://webassembly.github.io/spec/core/exec/runtime.html#store
type Store struct {
	_inner *C.wasm_store_t
	Engine *Engine
}

// NewStore instantiates a new Store with an Engine.
//
//	engine := NewEngine()
//	store := NewStore(engine)
func NewStore(engine *Engine) *Store {
	self := &Store{
		_inner: C.wasm_store_new(engine.inner()),
		Engine: engine,
	}

	runtime.SetFinalizer(self, func(self *Store) {
		self.Close()
	})

	return self
}

// CloneStore creates a new empty Store on the same Engine as store.
//
// This mirrors the "one instance, one store" pattern: Module/Engine stay
// shared on the pool, while each instance gets an independent Store arena
// that can be Close()'d without waiting for the pool template Store.
// Equivalent to NewStore(store.Engine); named CloneStore for API parity
// with forks that expose wasm_store_clone.
func CloneStore(store *Store) *Store {
	if store == nil || store.Engine == nil {
		panic("wasmer: CloneStore requires a non-nil Store with Engine")
	}
	return NewStore(store.Engine)
}

func (self *Store) inner() *C.wasm_store_t {
	return self._inner
}

func (self *Store) Inner() *C.wasm_store_t {
	return self._inner
}

// Close the store forcingly
//
// A runtime finalizer is registered on the Store, but it is possible
// to force the destruction of the Store by calling Close manually.
func (self *Store) Close() {
	if self == nil || self._inner == nil {
		return
	}
	runtime.SetFinalizer(self, nil)
	C.wasm_store_delete(self._inner)
	self._inner = nil
}

// DrainStackPool releases cached Wasm coroutine stacks in libwasmer.
// Call after instance teardown to reclaim residual anon mappings.
func DrainStackPool() {
	C.wasmer_drain_stack_pool()
}
