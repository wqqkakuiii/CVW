package main

import (
	"CVW/registry"
	"fmt"
	"math"
	"sync"
)

var GAS = &registry.Gas{}

//go:wasmexport SetGas
func SetGas(amount int32) { registry.SetGas(amount) }

//go:wasmexport GetGas
func GetGas() int32 { return registry.GetGas() }

type Person struct {
	Name string
	Age  int
}

type Shape interface {
	Area() float64
	Perimeter() float64
}

type Circle struct {
	Radius float64
}

func (c Circle) Area() float64 {
	registry.ConsumeGas(0)

	return math.Pi * c.Radius * c.Radius
}

func (c Circle) Perimeter() float64 {
	registry.ConsumeGas(0)

	return 2 * math.Pi * c.Radius
}

func (p *Person) SetAge(age int) {
	registry.ConsumeGas(0)

	p.Age = age
}

func (p Person) GetName() string {
	registry.ConsumeGas(0)

	return p.Name
}

//go:wasmexport Add
func Add(a, b int) int {
	registry.ConsumeGas(3)

	return a + b
}

//go:wasmexport Divide
func Divide(a, b int) (int, error) {
	registry.ConsumeGas(3)

	if b == 0 {
		registry.ConsumeGas(6)

		return 0, fmt.Errorf("division by zero")
	}
	return a / b, nil
}

//go:wasmexport Calculate
func Calculate(x, y int) (sum int, product int) {
	registry.ConsumeGas(5)

	sum = x + y
	product = x * y
	return
}

//go:wasmexport Sum
func Sum(numbers ...int) int {
	registry.ConsumeGas(10)

	total := 0
	for _, num := range numbers {
		registry.ConsumeGas(7)

		total += num
	}
	return total
}

//go:wasmexport MakeCounter
func MakeCounter() func() int {
	registry.ConsumeGas(5)

	count := 0
	return func() int {
		registry.ConsumeGas(1)

		count++
		return count
	}
}

//go:wasmexport ProcessData
func ProcessData(data []int) error {
	registry.ConsumeGas(1)

	if len(data) == 0 {
		registry.ConsumeGas(8)

		return fmt.Errorf("empty data")
	}
	for _, v := range data {
		registry.ConsumeGas(11)

		if v < 0 {
			registry.ConsumeGas(15)

			return fmt.Errorf("negative value: %d", v)
		}
	}
	return nil
}

//go:wasmexport DeferExample
func DeferExample() {
	registry.ConsumeGas(23)

	defer fmt.Println("deferred call")
	fmt.Println("normal call")
}

//go:wasmexport PanicRecoverExample
func PanicRecoverExample() {
	registry.ConsumeGas(1)

	defer func() {
		registry.ConsumeGas(3)

		if r := recover(); r != nil {
			registry.ConsumeGas(0)

			fmt.Printf("recovered from panic: %v\n", r)
		}
	}()
	panic("test panic")
}

//go:wasmexport IfElseExample
func IfElseExample(x int) string {
	registry.ConsumeGas(0)

	if x > 0 {
		registry.ConsumeGas(7)

		return "positive"
	} else if x < 0 {
		registry.ConsumeGas(1)

		return "negative"
	} else {
		registry.ConsumeGas(1)

		return "zero"
	}
}

//go:wasmexport SwitchExample
func SwitchExample(day int) string {
	registry.ConsumeGas(15)

	switch day {
	case 1:
		registry.ConsumeGas(1)

		return "Monday"
	case 2:
		registry.ConsumeGas(1)

		return "Tuesday"
	case 3, 4, 5:
		registry.ConsumeGas(1)

		return "Weekday"
	default:
		registry.ConsumeGas(1)

		return "Unknown"
	}
}

//go:wasmexport SwitchExpressionExample
func SwitchExpressionExample(x int) string {
	registry.ConsumeGas(6)

	switch {
	case x > 100:
		registry.ConsumeGas(1)

		return "large"
	case x > 10:
		registry.ConsumeGas(1)

		return "medium"
	default:
		registry.ConsumeGas(1)

		return "small"
	}
}

//go:wasmexport TypeSwitchExample
func TypeSwitchExample() {
	registry.ConsumeGas(16)

	var i interface{} = "hello"
	switch v := i.(type) {
	case string:
		registry.ConsumeGas(12)

		fmt.Printf("string type: %s\n", v)
	case int:
		registry.ConsumeGas(12)

		fmt.Printf("int type: %d\n", v)
	default:
		registry.ConsumeGas(3)

		fmt.Printf("unknown type\n")
	}
}

//go:wasmexport ForLoopExample
func ForLoopExample() {
	registry.ConsumeGas(0)

	for i := 0; i < 5; i++ {
		registry.ConsumeGas(19)

		fmt.Printf("i: %d\n", i)
	}
}

//go:wasmexport RangeExample
func RangeExample() {
	registry.ConsumeGas(22)

	arr := []int{1, 2, 3, 4, 5}
	for index, value := range arr {
		registry.ConsumeGas(28)

		fmt.Printf("index: %d, value: %d\n", index, value)
	}
}

//go:wasmexport MapExample
func MapExample() {
	registry.ConsumeGas(4)

	m := make(map[string]int)
	m["a"] = 1
	m["b"] = 2

	if val, ok := m["a"]; ok {
		registry.ConsumeGas(19)

		fmt.Printf("value: %d\n", val)
	}

	for key, value := range m {
		registry.ConsumeGas(24)

		fmt.Printf("key: %s, value: %d\n", key, value)
	}
}

//go:wasmexport SliceExample
func SliceExample() {
	registry.ConsumeGas(45)

	slice := []int{1, 2, 3}
	slice = append(slice, 4, 5)

	subSlice := slice[1:3]
	fmt.Printf("subSlice: %v\n", subSlice)

	newSlice := make([]int, 3, 5)
	copy(newSlice, slice)
}

//go:wasmexport ArrayExample
func ArrayExample() {
	registry.ConsumeGas(9)

	var arr [5]int
	arr[0] = 1
	arr[1] = 2

	for i := 0; i < len(arr); i++ {
		registry.ConsumeGas(28)

		fmt.Printf("arr[%d] = %d\n", i, arr[i])
	}
}

//go:wasmexport PointerExample
func PointerExample() {
	registry.ConsumeGas(18)

	x := 10
	p := &x
	*p = 20
	fmt.Printf("x: %d\n", x)
}

//go:wasmexport TypeAssertionExample
func TypeAssertionExample() {
	registry.ConsumeGas(14)

	var i interface{} = "hello"

	if s, ok := i.(string); ok {
		registry.ConsumeGas(21)

		fmt.Printf("string: %s\n", s)
	}

	switch v := i.(type) {
	case string:
		registry.ConsumeGas(12)

		fmt.Printf("string type: %s\n", v)
	case int:
		registry.ConsumeGas(12)

		fmt.Printf("int type: %d\n", v)
	default:
		registry.ConsumeGas(3)

		fmt.Printf("unknown type\n")
	}
}

//go:wasmexport TypeConversionExample
func TypeConversionExample() {
	registry.ConsumeGas(26)

	var x int = 42
	var y float64 = float64(x)
	var z int = int(y)
	fmt.Printf("x: %d, y: %f, z: %d\n", x, y, z)
}

//go:wasmexport GoroutineExample
func GoroutineExample() {
	registry.ConsumeGas(9)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		registry.ConsumeGas(3)

		defer wg.Done()
		fmt.Println("goroutine 1")
	}()

	go func() {
		registry.ConsumeGas(1)

		defer wg.Done()
		fmt.Println("goroutine 2")
	}()

	wg.Wait()
}

//go:wasmexport ChannelExample
func ChannelExample() {
	registry.ConsumeGas(7)

	ch := make(chan int, 2)

	ch <- 1
	ch <- 2

	close(ch)

	for val := range ch {
		registry.ConsumeGas(19)

		fmt.Printf("received: %d\n", val)
	}
}

//go:wasmexport SelectExample
func SelectExample() {
	registry.ConsumeGas(23)

	ch1 := make(chan int)
	ch2 := make(chan int)

	go func() {
		registry.ConsumeGas(3)

		ch1 <- 1
	}()

	go func() {
		registry.ConsumeGas(1)

		ch2 <- 2
	}()

	select {
	case val := <-ch1:
		registry.ConsumeGas(14)

		fmt.Printf("received from ch1: %d\n", val)
	case val := <-ch2:
		registry.ConsumeGas(14)

		fmt.Printf("received from ch2: %d\n", val)
	default:
		registry.ConsumeGas(12)

		fmt.Println("no message")
	}
}

//go:wasmexport NestedExample
func NestedExample() {
	registry.ConsumeGas(27)

	type Address struct {
		Street string
		City   string
	}

	type Employee struct {
		Name    string
		Address Address
	}

	emp := Employee{
		Name: "John",
		Address: Address{
			Street: "123 Main St",
			City:   "Beijing",
		},
	}

	fmt.Printf("Employee: %+v\n", emp)
}

//go:wasmexport LabelGotoExample
func LabelGotoExample() {
	registry.ConsumeGas(2)

	i := 0
Loop:
	if i < 5 {
		registry.ConsumeGas(17)

		fmt.Printf("i: %d\n", i)
		i++
		goto Loop
	}
}

//go:wasmexport ComplexControlFlow
func ComplexControlFlow() {
	registry.ConsumeGas(0)

	for i := 0; i < 10; i++ {
		registry.ConsumeGas(20)

		if i%2 == 0 {
			registry.ConsumeGas(5)

			continue
		}
		if i > 7 {
			registry.ConsumeGas(3)

			break
		}
		fmt.Printf("odd: %d\n", i)
	}
}

//go:wasmexport Factorial
func Factorial(n int) int {
	registry.ConsumeGas(7)

	if n <= 1 {
		registry.ConsumeGas(4)

		return 1
	}
	return n * Factorial(n-1)
}

type Calculator struct {
	result int
}

func (c *Calculator) Add(x int) *Calculator {
	registry.ConsumeGas(0)

	c.result += x
	return c
}

func (c *Calculator) Multiply(x int) *Calculator {
	registry.ConsumeGas(0)

	c.result *= x
	return c
}

func (c *Calculator) GetResult() int {
	registry.ConsumeGas(0)

	return c.result
}

type Reader interface {
	Read() []byte
}

type Writer interface {
	Write([]byte) error
}

type ReadWriter interface {
	Reader
	Writer
}

//go:wasmexport EmptyInterfaceExample
func EmptyInterfaceExample() {
	registry.ConsumeGas(20)

	var i interface{}
	i = 42
	i = "hello"
	i = []int{1, 2, 3}
	_ = i
}

//go:wasmexport FunctionAsValue
func FunctionAsValue() {
	registry.ConsumeGas(14)

	fn := func(x int) int {
		registry.ConsumeGas(0)

		return x * 2
	}

	result := fn(5)
	fmt.Printf("result: %d\n", result)
}

type Animal struct {
	Name string
}

func (a Animal) Speak() {
	registry.ConsumeGas(0)

	fmt.Printf("%s speaks\n", a.Name)
}

type Dog struct {
	Animal
	Breed string
}

func (d Dog) Speak() {
	registry.ConsumeGas(0)

	fmt.Printf("%s barks\n", d.Name)
}

//go:wasmexport NestedLoopExample
func NestedLoopExample() {
	registry.ConsumeGas(0)

	for i := 0; i < 5; i++ {
		registry.ConsumeGas(8)

		for j := 0; j < 5; j++ {
			registry.ConsumeGas(24)

			fmt.Printf("i: %d, j: %d\n", i, j)
		}
	}
}

func main() {
	registry.Register("gas", GAS)
	registry.ConsumeGas(0)

}
