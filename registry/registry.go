package registry

const gasKey = "gas"

// Value 是空接口
type Value interface{}

var (
	store  = make(map[string]Value)
	gasPtr *Gas // Gas 专用缓存
)

// Register 注册一个全局变量
func Register(name string, value Value) {
	if _, exists := store[name]; exists {
		panic("registry: name already registered")
	}
	store[name] = value
	if name == gasKey {
		if g, ok := value.(*Gas); ok {
			gasPtr = g
		}
	}
}

// Get 获取已注册的变量
func Get(name string) (Value, bool) {
	v, ok := store[name]
	return v, ok
}

// GetAs 泛型获取，若类型不匹配返回 false
func GetAs[T any](name string) (*T, bool) {
	v, ok := Get(name)
	if !ok {
		return nil, false
	}
	t, ok := v.(*T)
	return t, ok
}

// Gas 表示剩余 Gas
type Gas struct {
	Remain uint64
}

// ConsumeGas 消耗 Gas，不足时退出
func ConsumeGas(amount uint64) {
	if gasPtr == nil {
		return
	}
	if gasPtr.Remain < amount {
		panic("registry: no remaining gas")
	}
	gasPtr.Remain -= amount
}

// SetGas 设置 Gas 剩余量
func SetGas(amount uint64) {
	if gasPtr == nil {
		panic("registry: gas not initialized")
	}
	gasPtr.Remain = amount
}

// GetGas 获取当前 Gas 剩余量
func GetGas() uint64 {
	if gasPtr == nil {
		panic("registry: gas not initialized")
	}
	return gasPtr.Remain
}
