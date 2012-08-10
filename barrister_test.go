package barrister_test

import (
	. "github.com/coopernurse/barrister-go"
	"io/ioutil"
	"reflect"
	"testing"
	"fmt"
	"encoding/json"
)

func readFile(fname string) []byte {
	b, err := ioutil.ReadFile(fname)
	if err != nil {
		panic(err)
	}
	return b
}

func readConformJson() []byte {
	return readFile("test/conform.json")
}

func parseTestIdl() *Idl {
	idl, err := ParseIdlJson(readConformJson())
	if err != nil {
		panic(err)
	}
	return idl
}

func TestIdl2Go(t *testing.T) {
	idl := parseTestIdl()
	
	code := idl.GenerateGo("conform")
	ioutil.WriteFile("conform.go", code, 0644)
}

func TestParseIdlJson(t *testing.T) {
	idl := parseTestIdl()
	
	meta := Meta{BarristerVersion: "0.1.2", DateGenerated: 1337654725230000000, Checksum: "34f6238ed03c6319017382e0fdc638a7"}
	
	expected := Idl{Meta: meta}
	expected.Elems = append(expected.Elems, IdlJsonElem{Type: "comment", Value: "Barrister conformance IDL\n\nThe bits in here have silly names and the operations\nare not intended to be useful.  The intent is to\nexercise as much of the IDL grammar as possible"})

	enumVals := []EnumValue{
		EnumValue{Value: "ok"},
		EnumValue{Value: "err"},
	}
	expected.Elems = append(expected.Elems, 
		IdlJsonElem{Type: "enum", Name: "Status", Values: enumVals})

	enumVals2 := []EnumValue{
		EnumValue{Value: "add"},
		EnumValue{Value: "multiply", Comment: "mult comment"},
	}
	expected.Elems = append(expected.Elems, 
		IdlJsonElem{Type: "enum", Name: "MathOp", Values: enumVals2})

	fields := []Field{
		Field{Optional: false, IsArray: false, Type: "Status", Name: "status"},
	}
	expected.Elems = append(expected.Elems, IdlJsonElem{
		Type: "struct", Name: "Response", Fields: fields})

	fields2 := []Field{
		Field{Optional: false, IsArray: false, Type: "int", Name: "count"},
		Field{Optional: false, IsArray: true, Type: "string", Name: "items"},
	}
	expected.Elems = append(expected.Elems, 
		IdlJsonElem{Type: "struct", Name: "RepeatResponse", 
		Extends: "Response", Fields: fields2, 
	Comment: "testing struct inheritance"})
	
	if !reflect.DeepEqual(expected.Meta, idl.Meta) {
		t.Errorf("idl.Meta mismatch: %v != %v", expected.Meta, idl.Meta)
	}

	if len(idl.Elems) != 11 {
		t.Errorf("idl.Elems len %d != 11", len(idl.Elems))
	}

	for i, ex := range expected.Elems {
		if !reflect.DeepEqual(ex, idl.Elems[i]) {
			t.Errorf("idl.Elems[%d] mismatch: %v != %v", i, ex, idl.Elems[i])
		}
	}
}

///////////////////////////////

type B interface {
  // simply returns s 
  // if s == "return-null" then you should return a null 
  Echo(s string) *string 
}

type BImpl struct { }

func (b BImpl) Echo(s string) (*string, *JsonRpcError) {
	if s == "return-null" {
		return nil, nil
	}
	return &s, nil
}

type EchoCall struct {
	in  string
	out interface{}
}

func TestServerBarristerIdl(t *testing.T) {
	idl := parseTestIdl()
	svr := NewServer(idl)

	rpcReq := JsonRpcRequest{Id:"123", Method:"barrister-idl", Params:""}
	reqJson, _ := json.Marshal(rpcReq)
	respJson := svr.InvokeJson(reqJson)
	rpcResp := BarristerIdlRpcResponse{}
	err := json.Unmarshal(respJson, &rpcResp); if err != nil {
		panic(err)
	}

	fmt.Printf("%v\n", rpcResp.Result)

	if !reflect.DeepEqual(idl.Elems, rpcResp.Result) {
		t.Errorf("idl: %v != %v", idl.Elems, rpcResp.Result)
	}
}

func TestServerCallSuccess(t *testing.T) {
	bimpl := BImpl{}
	idl := parseTestIdl()
	svr := NewServer(idl)
	svr.AddHandler("B", bimpl)

	calls := []EchoCall{
		EchoCall{"hi", "hi"},
		EchoCall{"2", "2"},
		EchoCall{"return-null", nil},
	}

	for _, call := range(calls) {
		res, err := svr.Call("B.echo", call.in); if err != nil {
			panic(err)
		}

		resStr, ok := res.(*string); if !ok {
				s := fmt.Sprintf("B.echo return val cannot be converted to *string. type=%v", 
				reflect.TypeOf(res).Name())
			panic(s)
		}

		if !((resStr == nil && call.out == nil) || (*resStr == call.out)) {
			t.Errorf("B.echo %v != %v", resStr, call.out)
		}
	}
}

type CallFail struct {
	method  string
	errcode int
}

func TestServerCallFail(t *testing.T) {
	bimpl := BImpl{}
	idl := parseTestIdl()
	svr := NewServer(idl)
	svr.AddHandler("B", bimpl)

	calls := []CallFail{
		CallFail{"B.", -32601},
		CallFail{"", -32601},
		CallFail{"B.foo", -32601},
		CallFail{"B.Echo", -32602},
	}

	for _, call := range(calls) {
		res, err := svr.Call(call.method)
		if res != nil {
			t.Errorf("%v != nil on expected fail call: %s", res, call.method)
		} else if err == nil {
			t.Errorf("err == nil on expected fail call: %s", call.method)
		} else if err.Code != call.errcode {
			t.Errorf("errcode %d != %d on expected fail call: %s", err.Code, 
				call.errcode, call.method)
		}
	}
}

func TestParseMethod(t *testing.T) {
	cases := [][]string{
		[]string{ "B.echo", "B", "Echo"},
		[]string{ "B.", "B.", ""},
		[]string{ "Cat.a", "Cat", "A"},
		[]string{ "barrister-idl", "barrister-idl", ""},
	}

	for _, c := range(cases) {
		iface, fname := ParseMethod(c[0])
		if (iface != c[1]) {
			t.Errorf("%s != %s for input: %s", iface, c[1], c[0])
		}
		if (fname != c[2]) {
			t.Errorf("%s != %s for input: %s", fname, c[2], c[0])
		}
	}
}