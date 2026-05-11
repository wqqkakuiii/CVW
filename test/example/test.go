package main

import (
	"fmt"
	"math"
	"sync"
)

// 结构体定义
type Person struct {
	Name string
	Age  int
}

// 接口定义
type Shape interface {
	Area() float64
	Perimeter() float64
}

// 结构体实现接口
type Circle struct {
	Radius float64
}

func (c Circle) Area() float64 {
	return math.Pi * c.Radius * c.Radius
}

func (c Circle) Perimeter() float64 {
	return 2 * math.Pi * c.Radius
}

// 指针接收者方法
func (p *Person) SetAge(age int) {
	p.Age = age
}

// 值接收者方法
func (p Person) GetName() string {
	return p.Name
}

// 基本函数
func Add(a, b int) int {
	return a + b
}

// 多返回值函数
func Divide(a, b int) (int, error) {
	if b == 0 {
		return 0, fmt.Errorf("division by zero")
	}
	return a / b, nil
}

// 命名返回值
func Calculate(x, y int) (sum int, product int) {
	sum = x + y
	product = x * y
	return
}

// 可变参数函数
func Sum(numbers ...int) int {
	total := 0
	for _, num := range numbers {
		total += num
	}
	return total
}

// 闭包
func MakeCounter() func() int {
	count := 0
	return func() int {
		count++
		return count
	}
}

// 错误处理
func ProcessData(data []int) error {
	if len(data) == 0 {
		return fmt.Errorf("empty data")
	}
	for _, v := range data {
		if v < 0 {
			return fmt.Errorf("negative value: %d", v)
		}
	}
	return nil
}

// defer 使用
func DeferExample() {
	defer fmt.Println("deferred call")
	fmt.Println("normal call")
}

// panic 和 recover
func PanicRecoverExample() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("recovered from panic: %v\n", r)
		}
	}()
	panic("test panic")
}

// if-else 语句
func IfElseExample(x int) string {
	if x > 0 {
		return "positive"
	} else if x < 0 {
		return "negative"
	} else {
		return "zero"
	}
}

// switch 语句
func SwitchExample(day int) string {
	switch day {
	case 1:
		return "Monday"
	case 2:
		return "Tuesday"
	case 3, 4, 5:
		return "Weekday"
	default:
		return "Unknown"
	}
}

// switch 表达式
func SwitchExpressionExample(x int) string {
	switch {
	case x > 100:
		return "large"
	case x > 10:
		return "medium"
	default:
		return "small"
	}
}

// type switch
func TypeSwitchExample() {
	var i interface{} = "hello"
	switch v := i.(type) {
	case string:
		fmt.Printf("string type: %s\n", v)
	case int:
		fmt.Printf("int type: %d\n", v)
	default:
		fmt.Printf("unknown type\n")
	}
}

// for 循环
func ForLoopExample() {
	for i := 0; i < 5; i++ {
		fmt.Printf("i: %d\n", i)
	}
}

// range 循环
func RangeExample() {
	arr := []int{1, 2, 3, 4, 5}
	for index, value := range arr {
		fmt.Printf("index: %d, value: %d\n", index, value)
	}
}

// map 操作
func MapExample() {
	m := make(map[string]int)
	m["a"] = 1
	m["b"] = 2

	if val, ok := m["a"]; ok {
		fmt.Printf("value: %d\n", val)
	}

	for key, value := range m {
		fmt.Printf("key: %s, value: %d\n", key, value)
	}
}

// 切片操作
func SliceExample() {
	slice := []int{1, 2, 3}
	slice = append(slice, 4, 5)

	subSlice := slice[1:3]
	fmt.Printf("subSlice: %v\n", subSlice)

	newSlice := make([]int, 3, 5)
	copy(newSlice, slice)
}

// 数组操作
func ArrayExample() {
	var arr [5]int
	arr[0] = 1
	arr[1] = 2

	for i := 0; i < len(arr); i++ {
		fmt.Printf("arr[%d] = %d\n", i, arr[i])
	}
}

// 指针操作
func PointerExample() {
	x := 10
	p := &x
	*p = 20
	fmt.Printf("x: %d\n", x)
}

// 类型断言
func TypeAssertionExample() {
	var i interface{} = "hello"

	if s, ok := i.(string); ok {
		fmt.Printf("string: %s\n", s)
	}

	switch v := i.(type) {
	case string:
		fmt.Printf("string type: %s\n", v)
	case int:
		fmt.Printf("int type: %d\n", v)
	default:
		fmt.Printf("unknown type\n")
	}
}

// 类型转换
func TypeConversionExample() {
	var x int = 42
	var y float64 = float64(x)
	var z int = int(y)
	fmt.Printf("x: %d, y: %f, z: %d\n", x, y, z)
}

// goroutine
func GoroutineExample() {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		fmt.Println("goroutine 1")
	}()

	go func() {
		defer wg.Done()
		fmt.Println("goroutine 2")
	}()

	wg.Wait()
}

// channel
func ChannelExample() {
	ch := make(chan int, 2)

	ch <- 1
	ch <- 2

	close(ch)

	for val := range ch {
		fmt.Printf("received: %d\n", val)
	}
}

// select 语句
func SelectExample() {
	ch1 := make(chan int)
	ch2 := make(chan int)

	go func() {
		ch1 <- 1
	}()

	go func() {
		ch2 <- 2
	}()

	select {
	case val := <-ch1:
		fmt.Printf("received from ch1: %d\n", val)
	case val := <-ch2:
		fmt.Printf("received from ch2: %d\n", val)
	default:
		fmt.Println("no message")
	}
}

// 嵌套结构
func NestedExample() {
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

// 标签和 goto
func LabelGotoExample() {
	i := 0
Loop:
	if i < 5 {
		fmt.Printf("i: %d\n", i)
		i++
		goto Loop
	}
}

// 复杂控制流
func ComplexControlFlow() {
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			continue
		}
		if i > 7 {
			break
		}
		fmt.Printf("odd: %d\n", i)
	}
}

// 递归函数
func Factorial(n int) int {
	if n <= 1 {
		return 1
	}
	return n * Factorial(n-1)
}

// 方法链
type Calculator struct {
	result int
}

func (c *Calculator) Add(x int) *Calculator {
	c.result += x
	return c
}

func (c *Calculator) Multiply(x int) *Calculator {
	c.result *= x
	return c
}

func (c *Calculator) GetResult() int {
	return c.result
}

// 接口组合
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

// 空接口
func EmptyInterfaceExample() {
	var i interface{}
	i = 42
	i = "hello"
	i = []int{1, 2, 3}
	_ = i
}

// 函数作为值
func FunctionAsValue() {
	fn := func(x int) int {
		return x * 2
	}

	result := fn(5)
	fmt.Printf("result: %d\n", result)
}

// 结构体嵌入
type Animal struct {
	Name string
}

func (a Animal) Speak() {
	fmt.Printf("%s speaks\n", a.Name)
}

type Dog struct {
	Animal
	Breed string
}

// 方法重写
func (d Dog) Speak() {
	fmt.Printf("%s barks\n", d.Name)
}

// 嵌套循环
func NestedLoopExample() {
	for i := 0; i < 5; i++ {
		for j := 0; j < 5; j++ {
			fmt.Printf("i: %d, j: %d\n", i, j)
		}
	}
}

// 主函数（空）
func main() {
}
