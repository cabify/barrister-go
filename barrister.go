package barrister

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"reflect"
	"strings"
	"time"
)

var zeroVal reflect.Value

func EncodeASCII(b []byte) (*bytes.Buffer, error) {
	in := bytes.NewBuffer(b)
	out := bytes.NewBufferString("")
	for {
		r, size, err := in.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if size == 1 {
			out.WriteRune(r)
		} else if size == 2 {
			out.WriteString(fmt.Sprintf("\\u%04x", r))
		} else {
			out.WriteString(fmt.Sprintf("\\U%08x", r))
		}
	}
	return out, nil
}

//////////////////////////////////////////////////
// IDL //
/////////

func ParseIdlJson(jsonData []byte) (*Idl, error) {

	elems := []IdlJsonElem{}
	err := json.Unmarshal(jsonData, &elems)
	if err != nil {
		return nil, err
	}

	return NewIdl(elems), nil
}

func NewIdl(elems []IdlJsonElem) *Idl {
	idl := &Idl{
		elems:      elems,
		interfaces: map[string][]Function{},
		methods:    map[string]Function{},
		structs:    map[string]*Struct{},
		enums:      map[string][]EnumValue{},
	}

	for _, el := range elems {
		if el.Type == "meta" {
			idl.Meta = Meta{el.BarristerVersion, el.DateGenerated * 1000000, el.Checksum}
		} else if el.Type == "interface" {
			funcs := []Function{}
			for _, f := range el.Functions {
				meth := fmt.Sprintf("%s.%s", el.Name, f.Name)
				idl.methods[meth] = f
				funcs = append(funcs, f)
			}
			idl.interfaces[el.Name] = funcs
		} else if el.Type == "struct" {
			idl.structs[el.Name] = &Struct{Name: el.Name, Extends: el.Extends, Fields: el.Fields}
		} else if el.Type == "enum" {
			idl.enums[el.Name] = el.Values
		}
	}

	idl.computeAllStructFields()

	return idl
}

type IdlJsonElem struct {
	// common fields
	Type    string `json:"type"`
	Name    string `json:"name"`
	Comment string `json:"comment"`

	// type=comment
	Value string `json:"value"`

	// type=struct
	Extends string  `json:"extends"`
	Fields  []Field `json:"fields"`

	// type=enum
	Values []EnumValue `json:"values"`

	// type=interface
	Functions []Function `json:"functions"`

	// type=meta
	BarristerVersion string `json:"barrister_version"`
	DateGenerated    int64  `json:"date_generated"`
	Checksum         string `json:"checksum"`
}

type Function struct {
	Name    string  `json:"name"`
	Comment string  `json:"comment"`
	Params  []Field `json:"params"`
	Returns Field   `json:"returns"`
}

type Struct struct {
	Name    string
	Extends string
	Fields  []Field

	// fields in this struct, and its parents
	// hashed by Field.Name
	allFields map[string]Field
}

type Field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Optional bool   `json:"optional"`
	IsArray  bool   `json:"is_array"`
	Comment  string `json:"comment"`
}

func (f Field) goType(optionalToPtr bool) string {
	if f.IsArray {
		f2 := Field{f.Name, f.Type, f.Optional, false, ""}
		return "[]" + f2.goType(optionalToPtr)
	}

	prefix := ""
	if optionalToPtr && f.Optional {
		prefix = "*"
	}

	switch f.Type {
	case "string":
		return prefix + "string"
	case "int":
		return prefix + "int64"
	case "float":
		return prefix + "float64"
	case "bool":
		return prefix + "bool"
	}

	return prefix + f.Type
}

func (f Field) zeroVal(idl *Idl, optionalToPtr bool) interface{} {

	if f.Optional && optionalToPtr {
		return "nil"
	}

	if f.IsArray {
		return f.goType(false) + "{}"
	}

	switch f.Type {
	case "string":
		return `""`
	case "int":
		return "int64(0)"
	case "float":
		return "float64(0)"
	case "bool":
		return "false"
	}

	s, ok := idl.structs[f.Type]
	if ok {
		return capitalize(s.Name) + "{}"
	}

	e, ok := idl.enums[f.Type]
	if ok && len(e) > 0 {
		return `""`
	}

	msg := fmt.Sprintf("Unable to create val for field: %s type: %s",
		f.Name, f.Type)
	panic(msg)
}

func (f Field) testVal(idl *Idl) interface{} {

	if f.IsArray {
		f2 := Field{f.Name, f.Type, f.Optional, false, ""}
		arr := []interface{}{}
		arr = append(arr, f2.testVal(idl))
		return arr
	}

	switch f.Type {
	case "string":
		return "testval"
	case "int":
		return int64(99)
	case "float":
		return float64(10.3)
	case "bool":
		return true
	}

	s, ok := idl.structs[f.Type]
	if ok {
		val := map[string]interface{}{}
		for _, f2 := range s.allFields {
			val[f2.Name] = f2.testVal(idl)
		}
		return val
	}

	e, ok := idl.enums[f.Type]
	if ok && len(e) > 0 {
		return e[0].Value
	}

	msg := fmt.Sprintf("Unable to create val for field: %s type: %s",
		f.Name, f.Type)
	panic(msg)
}

type EnumValue struct {
	Value   string `json:"value"`
	Comment string `json:"comment"`
}

type Meta struct {
	BarristerVersion string
	DateGenerated    int64
	Checksum         string
}

type Idl struct {
	// raw data from IDL file
	elems []IdlJsonElem

	// meta information about the contract
	Meta Meta

	// hashed elements
	interfaces map[string][]Function
	methods    map[string]Function
	structs    map[string]*Struct
	enums      map[string][]EnumValue
}

func (idl *Idl) computeAllStructFields() {
	for _, s := range idl.structs {
		s.allFields = idl.computeStructFields(s, map[string]Field{})
	}
}

func (idl *Idl) computeStructFields(toAdd *Struct, allFields map[string]Field) map[string]Field {
	for _, f := range toAdd.Fields {
		allFields[f.Name] = f
	}

	if toAdd.Extends != "" {
		parent, ok := idl.structs[toAdd.Extends]
		if ok {
			allFields = idl.computeStructFields(parent, allFields)
		}
	}

	return allFields
}

func (idl *Idl) GenerateGo(pkgName string, optionalToPtr bool) []byte {
	b := &bytes.Buffer{}
	line(b, 0, fmt.Sprintf("package %s\n", pkgName))
	line(b, 0, "import (")
	line(b, 1, `"fmt"`)
	line(b, 1, `"reflect"`)
	line(b, 1, `"github.com/coopernurse/barrister-go"`)
	line(b, 0, ")\n")

	for name, en := range idl.enums {
		goName := capitalize(name)
		line(b, 0, fmt.Sprintf("type %s string", goName))
		line(b, 0, "const (")
		for x, val := range en {
			typeStr := ""
			if x == 0 {
				typeStr = goName
			}
			line(b, 1, fmt.Sprintf("%s%s %s = \"%s\"", 
				goName, capitalize(val.Value), typeStr, val.Value))
		}
		line(b, 0, ")\n")
	}

	for _, s := range idl.structs {
		goName := capitalize(s.Name)
		line(b, 0, fmt.Sprintf("type %s struct {", goName))
		for _, f := range s.allFields {
			goName = capitalize(f.Name)
			omit := ""
			if f.Optional {
				omit = ",omitempty"
			}
			line(b, 1, fmt.Sprintf("%s\t%s\t`json:\"%s%s\"`", 
				goName, f.goType(optionalToPtr), f.Name, omit))
		}
		line(b, 0, "}\n")
	}
	line(b, 0, "")

	for name, funcs := range idl.interfaces {
		goName := capitalize(name)
		line(b, 0, fmt.Sprintf("type %s interface {", goName))
		for _, fn := range funcs {
			goName = capitalize(fn.Name)
			params := ""
			for x, p := range fn.Params {
				if x > 0 {
					params += ", "
				}
				params += fmt.Sprintf("%s %s", p.Name, p.goType(optionalToPtr))
			}
			line(b, 1, fmt.Sprintf("%s(%s) (%s, *barrister.JsonRpcError)", 
				goName, params, fn.Returns.goType(optionalToPtr)))
		}
		line(b, 0, "}\n")

		goName = goName + "Proxy"
		line(b, 0, fmt.Sprintf("type %s struct {", goName))
		line(b, 1, "client barrister.Client")
		line(b, 0, "}\n")
		for _, fn := range funcs {
			method := fmt.Sprintf("%s.%s", name, fn.Name)
			retType := fn.Returns.goType(optionalToPtr)
			zeroVal := fn.Returns.zeroVal(idl, optionalToPtr)
			fnName := capitalize(fn.Name)
			params := ""
			paramIdents := ""
			for x, p := range fn.Params {
				if x > 0 {
					params += ", "
				}
				params += fmt.Sprintf("%s %s", p.Name, p.goType(optionalToPtr))
				paramIdents += ", "
				paramIdents += p.Name
			}
			line(b, 0, fmt.Sprintf("func (_p %s) %s(%s) (%s, *barrister.JsonRpcError) {", 
				goName, fnName, params, retType))
			line(b, 1, fmt.Sprintf("_res, _err := _p.client.Call(\"%s\"%s)", 
				method, paramIdents))
			line(b, 1, "if _err == nil {")
			if optionalToPtr && fn.Returns.Optional {
				line(b, 2, "if _res == nil {")
				line(b, 3, "return nil, nil")
				line(b, 2, "}")
			}
			line(b, 2, fmt.Sprintf("_cast, _ok := _res.(%s)", retType))
			line(b, 2, "if !_ok {")
			line(b, 3, "_t := reflect.TypeOf(_res)")
			line(b, 3, `_msg := fmt.Sprintf("`+method+` returned invalid type: %v", _t)`)
			line(b, 3, fmt.Sprintf("return %s, &barrister.JsonRpcError{Code: -32000, Message: _msg}", zeroVal))
			line(b, 2, "}")
			line(b, 2, "return _cast, nil")
			line(b, 1, "}")
			line(b, 1, fmt.Sprintf("return %s, _err", zeroVal))
			line(b, 0, "}\n")
		}
	}

	return b.Bytes()
}

func comment(b *bytes.Buffer, level int, comment string) {
	if comment != "" {
		for _, ln := range strings.Split(comment, "\n") {
			line(b, level, fmt.Sprintf("// %s", ln))
		}
	}
}

func line(b *bytes.Buffer, level int, s string) {
	for i := 0; i < level; i++ {
		b.WriteString("\t")
    }		
	b.WriteString(s)
	b.WriteString("\n")
}

//////////////////////////////////////////////////
// Request / Response //
////////////////////////

type JsonRpcRequest struct {
	Jsonrpc string      `json:"jsonrpc"`
	Id      string      `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type JsonRpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (e *JsonRpcError) Error() string {
	return fmt.Sprintf("JsonRpcError: code: %d message: %s", e.Code, e.Message)
}

type JsonRpcResponse struct {
	Jsonrpc string        `json:"jsonrpc"`
	Id      string        `json:"id"`
	Error   *JsonRpcError `json:"error,omitempty"`
	Result  interface{}   `json:"result,omitempty"`
}

type BarristerIdlRpcResponse struct {
	Id     string        `json:"id"`
	Error  *JsonRpcError `json:"error,omitempty"`
	Result []IdlJsonElem `json:"result,omitempty"`
}

//////////////////////////////////////////////////
// Client //
////////////

type Serializer interface {
	Marshal(in interface{}) ([]byte, error)
	Unmarshal(in []byte, out interface{}) error
}

type JsonSerializer struct { 
	ForceASCII    bool
}

func (s *JsonSerializer) Marshal(in interface{}) ([]byte, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	
	if s.ForceASCII {
		buf, err := EncodeASCII(b)
		if err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	return b, nil
}

func (s *JsonSerializer) Unmarshal(in []byte, out interface{}) error {
	return json.Unmarshal(in, out)
}

type Transport interface {
	Send(in []byte) ([]byte, error)
}

type HttpTransport struct {
	Url string
}

func (t *HttpTransport) Send(in []byte) ([]byte, error) {

	//fmt.Printf("request:\n%s\n", post)

	req, err := http.NewRequest("POST", t.Url, bytes.NewBuffer(in))
	if err != nil {
		msg := fmt.Sprintf("barrister: HttpTransport NewRequest failed: %s", err)
		return nil, errors.New(msg)
	}

	req.Header.Add("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		msg := fmt.Sprintf("barrister: HttpTransport POST to %s failed: %s", t.Url, err)
		return nil, errors.New(msg)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		msg := fmt.Sprintf("barrister: HttpTransport Unable to read resp.Body: %s", err)
		return nil, errors.New(msg)
	}

	//fmt.Printf("%s\n\n", body)

	return body, nil
}

type Client interface {
	Call(method string, params ...interface{}) (interface{}, *JsonRpcError)
	CallBatch(batch []JsonRpcRequest) []JsonRpcResponse
}

type RemoteClient struct {
	trans Transport
	ser   Serializer
}

func (c *RemoteClient) CallBatch(batch []JsonRpcRequest) []JsonRpcResponse {
	reqBytes, err := c.ser.Marshal(batch)
	if err != nil {
		msg := fmt.Sprintf("barrister: CallBatch unable to Marshal request: %s", err)
		return []JsonRpcResponse{
			JsonRpcResponse{Error: &JsonRpcError{Code: -32600, Message: msg} }}
	}

	respBytes, err := c.trans.Send(reqBytes)
	if err != nil {
		msg := fmt.Sprintf("barrister: CallBatch Transport error during request: %s", err)
		return []JsonRpcResponse{
			JsonRpcResponse{Error: &JsonRpcError{Code: -32603, Message: msg} }}
	}

	var batchResp []JsonRpcResponse
	err = c.ser.Unmarshal(respBytes, &batchResp)
	if err != nil {
		msg := fmt.Sprintf("barrister: CallBatch unable to Unmarshal response: %s", err)
		return []JsonRpcResponse{
			JsonRpcResponse{Error: &JsonRpcError{Code: -32603, Message: msg} }}
	}

	return batchResp
}

func (c *RemoteClient) Call(method string, params ...interface{}) (interface{}, *JsonRpcError) {
	rpcReq := JsonRpcRequest{Jsonrpc: "2.0", Id: randStr(20), Method: method, Params: params}

	reqBytes, err := c.ser.Marshal(rpcReq)
	if err != nil {
		msg := fmt.Sprintf("barrister: %s: Call unable to Marshal request: %s", method, err)
		return nil, &JsonRpcError{Code: -32600, Message: msg}
	}

	respBytes, err := c.trans.Send(reqBytes)
	if err != nil {
		msg := fmt.Sprintf("barrister: %s: Transport error during request: %s", method, err)
		return nil, &JsonRpcError{Code: -32603, Message: msg}
	}

	var rpcResp JsonRpcResponse
	err = c.ser.Unmarshal(respBytes, &rpcResp)
	if err != nil {
		msg := fmt.Sprintf("barrister: %s: Call unable to Unmarshal response: %s", method, err)
		return nil, &JsonRpcError{Code: -32603, Message: msg}
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}

	return rpcResp.Result, nil
}

//////////////////////////////////////////////////
// Server //
////////////

func NewServer(idl *Idl) Server {
	return Server{idl, map[string]interface{}{}}
}

type Server struct {
	idl      *Idl
	handlers map[string]interface{}
}

func (s *Server) AddHandler(iface string, impl interface{}) {
	ifaceFuncs, ok := s.idl.interfaces[iface]

	if !ok {
		msg := fmt.Sprintf("barrister: IDL has no interface: %s", iface)
		panic(msg)
	}

	rpcErrKind := reflect.TypeOf(JsonRpcError{}).Kind()

	elem := reflect.ValueOf(impl)
	for _, idlFunc := range ifaceFuncs {
		fname := capitalize(idlFunc.Name)
		fn := elem.MethodByName(fname)
		if fn == zeroVal {
			msg := fmt.Sprintf("barrister: %s impl has no method named: %s",
				iface, fname)
			panic(msg)
		}

		fnType := fn.Type()
		if fnType.NumIn() != len(idlFunc.Params) {
			msg := fmt.Sprintf("barrister: %s impl method: %s accepts %d params but IDL specifies %d", iface, fname, fnType.NumIn(), len(idlFunc.Params))
			panic(msg)
		}

		if fnType.NumOut() != 2 {
			msg := fmt.Sprintf("barrister: %s impl method: %s returns %d params but must be 2", iface, fname, fnType.NumOut())
			panic(msg)
		}

		for x, param := range idlFunc.Params {
			path := fmt.Sprintf("%s.%s param[%d]", iface, fname, x)
			s.validate(param, fnType.In(x), path)
		}

		path := fmt.Sprintf("%s.%s return value[0]", iface, fname)
		s.validate(idlFunc.Returns, fnType.Out(0), path)

		errType := fnType.Out(1)
		if errType.Kind() != reflect.Ptr || errType.Elem().Kind() != rpcErrKind {
			msg := fmt.Sprintf("%s.%s return value[1] has invalid type: %s (expected: *barrister.JsonRpcError)", iface, fname, errType)
			panic(msg)
		}
	}

	s.handlers[iface] = impl
}

func (s *Server) validate(idlField Field, implType reflect.Type, path string) {
	testVal := idlField.testVal(s.idl)
	conv := NewConvert(s.idl, &idlField, implType, testVal, "")
	_, err := conv.Run()
	if err != nil {
		msg := fmt.Sprintf("barrister: %s has invalid type: %s reason: %s", path, implType, err)
		panic(msg)
	}
}

func (s *Server) InvokeJSON(j []byte) []byte {

	// determine if batch or single
	batch := false
	for i := 0; i < len(j); i++ {
		if j[i] == '{' {
			break
		} else if j[i] == '[' {
			batch = true
			break
		}
	}

	if batch {
		var batchReq []JsonRpcRequest
		batchResp := []JsonRpcResponse{}
		err := json.Unmarshal(j, &batchReq)
		if err != nil {
			return jsonParseErr("", err)
		}

		for _, req := range batchReq {
			resp := s.InvokeOne(&req)
			batchResp = append(batchResp, *resp)
		}

		b, _ := json.Marshal(batchResp)
		if err != nil {
			panic(err)
		}
		return b
	}

	//  - parse json into JsonRpcRequest
	rpcReq := JsonRpcRequest{}
	err := json.Unmarshal(j, &rpcReq)
	if err != nil {
		return jsonParseErr("", err)
	}

	resp := s.InvokeOne(&rpcReq)

	b, _ := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	return b
}

func (s *Server) InvokeOne(rpcReq *JsonRpcRequest) *JsonRpcResponse {
	var rpcerr *JsonRpcError

	if rpcReq.Method == "barrister-idl" {
		// handle 'barrister-idl' method
		return &JsonRpcResponse{Jsonrpc: "2.0", Id: rpcReq.Id, Result: s.idl.elems}
	} else {
		// handle normal RPC method executions
		var result interface{}
		arr, ok := rpcReq.Params.([]interface{})
		if ok {
			result, rpcerr = s.Call(rpcReq.Method, arr...)
		} else {
			result, rpcerr = s.Call(rpcReq.Method, rpcReq.Params)
		}
		if rpcerr == nil {
			// successful Call
			return &JsonRpcResponse{Jsonrpc: "2.0", Id: rpcReq.Id, Result: result}
		}
	}

	// RPC error occurred
	return &JsonRpcResponse{Jsonrpc: "2.0", Id: rpcReq.Id, Error: rpcerr}
}

func (s *Server) CallBatch(batch []JsonRpcRequest) []JsonRpcResponse {
	batchResp := make([]JsonRpcResponse, len(batch))

	for _, req := range batch {
		result, err := s.Call(req.Method, req.Params)
		resp := JsonRpcResponse{Jsonrpc: "2.0", Id: req.Id}
		if err == nil {
			resp.Result = result
		} else {
			resp.Error = err
		}
		batchResp = append(batchResp, resp)
	}

	return batchResp
}

func (s *Server) Call(method string, params ...interface{}) (interface{}, *JsonRpcError) {

	idlFunc, ok := s.idl.methods[method]
	if !ok {
		return nil, &JsonRpcError{Code: -32601, Message: fmt.Sprintf("Unsupported method: %s", method)}
	}

	iface, fname := ParseMethod(method)

	handler, ok := s.handlers[iface]
	if !ok {
		return nil, &JsonRpcError{Code: -32601, Message: fmt.Sprintf("No handler registered for interface: %s", iface)}
	}

	elem := reflect.ValueOf(handler)
	fn := elem.MethodByName(fname)
	if fn == zeroVal {
		return nil, &JsonRpcError{Code: -32601, Message: fmt.Sprintf("Function %s not found on handler %s", fname, iface)}
	}

	// check params
	fnType := fn.Type()
	if fnType.NumIn() != len(params) {
		return nil, &JsonRpcError{Code: -32602, Message: fmt.Sprintf("Method %s expects %d params but was passed %d", method, fnType.NumIn(), len(params))}
	}

	if len(idlFunc.Params) != len(params) {
		return nil, &JsonRpcError{Code: -32602, Message: fmt.Sprintf("Method %s expects %d params but was passed %d", method, len(idlFunc.Params), len(params))}
	}

	// convert params
	paramVals := []reflect.Value{}
	for x, param := range params {
		desiredType := fnType.In(x)
		idlField := idlFunc.Params[x]
		path := fmt.Sprintf("param[%d]", x)
		paramConv := NewConvert(s.idl, &idlField, desiredType, param, path)
		converted, err := paramConv.Run()
		if err != nil {
			return nil, &JsonRpcError{Code: -32602, Message: err.Error()}
		}
		paramVals = append(paramVals, converted)
		//fmt.Printf("%s - %v\n", path, reflect.TypeOf(converted.Interface()))
	}

	// make the call
	ret := fn.Call(paramVals)
	if len(ret) != 2 {
		return nil, &JsonRpcError{Code: -32603, Message: fmt.Sprintf("Method %s did not return 2 values. len(ret)=%d", method, len(ret))}
	}

	ret0 := ret[0].Interface()
	ret1 := ret[1].Interface()

	if ret1 != nil {
		rpcErr, ok := ret1.(*JsonRpcError)
		if !ok {
			return nil, &JsonRpcError{Code: -32603, Message: fmt.Sprintf("Method %s did not return JsonRpcError for last return val: %v", method, ret1)}
		}
		return ret0, rpcErr
	}

	//err = s.idl.ValidateResult(method, ret0)
	//if err != nil {
	//	return nil, err
	//}

	return ret0, nil
}

func ParseMethod(method string) (string, string) {
	i := strings.Index(method, ".")
	if i > -1 && i < (len(method)-1) {
		iface := method[0:i]
		if i < (len(method) - 2) {
			return iface, strings.ToUpper(method[i+1:i+2]) + method[i+2:]
		} else {
			return iface, strings.ToUpper(method[i+1:])
		}
	}
	return method, ""
}

func jsonParseErr(reqId string, err error) []byte {
	rpcerr := &JsonRpcError{Code: -32700, Message: fmt.Sprintf("Unable to parse JSON: %s", err.Error())}
	resp := JsonRpcResponse{Jsonrpc: "2.0"}
	resp.Id = reqId
	resp.Error = rpcerr
	b, _ := json.Marshal(resp)
	return b
}

func randStr(length int) string {
	rand.Seed(time.Now().UnixNano())
	b := bytes.Buffer{}
	for i := 0; i < length; i++ {
		x := rand.Int31n(36)
		if x < 10 {
			b.WriteString(string(48 + x))
		} else {
			b.WriteString(string(87 + x))
		}
	}
	return b.String()
}
