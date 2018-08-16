#!/bin/bash

context="$1"
server_file="$2"

context_import=""
case $context in
yes|both)
	context_import='"context"'
	;;
esac

echo 'package main

import (
	"flag"
	"fmt"
	"github.com/coopernurse/barrister-go"
	. "github.com/coopernurse/barrister-go/conform/generated/conform"
	"github.com/coopernurse/barrister-go/conform/generated/inc"
	"math"
	"net/http"
	"strings"
	'"$context_import"'
)
' > $server_file

function generate_impls {
	local suffix=$1
	local context_arg_no_comma=$2
	local context_arg=""
	if [ -n "$context_arg_no_comma" ]; then
		context_arg="$context_arg_no_comma, "
	fi
	echo '
type AImpl'"$suffix"' struct{}

func (i AImpl'"$suffix"') Add('"$context_arg"'a int64, b int64) (int64, error) {
	return a + b, nil
}

func (a AImpl'"$suffix"') Calc('"$context_arg"'nums []float64, operation inc.MathOp) (float64, error) {
	switch operation {
	case inc.MathOpAdd:
		sum := float64(0)
		for i := 0; i < len(nums); i++ {
			sum += nums[i]
		}
		return sum, nil
	case inc.MathOpMultiply:
		x := float64(1)
		for i := 0; i < len(nums); i++ {
			x = x * nums[i]
		}
		return x, nil
	}

	msg := fmt.Sprintf("Unknown operation: %s", operation)
	return 0, &barrister.JsonRpcError{Code: -32000, Message: msg}
}

// returns the square root of a
func (i AImpl'"$suffix"') Sqrt('"$context_arg"'a float64) (float64, error) {
	return math.Sqrt(a), nil
}

// Echos the req1.to_repeat string as a list,
// optionally forcing to_repeat to upper case
//
// RepeatResponse.items should be a list of strings
// whose length is equal to req1.count
func (a AImpl'"$suffix"') Repeat('"$context_arg"'req1 RepeatRequest) (RepeatResponse, error) {
	rr := RepeatResponse{inc.Response{"ok"}, req1.Count, []string{}}

	s := req1.To_repeat
	if req1.Force_uppercase {
		s = strings.ToUpper(s)
	}
	for i := int64(0); i < req1.Count; i++ {
		rr.Items = append(rr.Items, s)
	}

	return rr, nil
}

//
// returns a result with:
//   hi="hi" and status="ok"
func (a AImpl'"$suffix"') Say_hi('"$context_arg_no_comma"') (HiResponse, error) {
	return HiResponse{"hi"}, nil
}

// returns num as an array repeated '"'"'count'"'"' number of times
func (a AImpl'"$suffix"') Repeat_num('"$context_arg"'num int64, count int64) ([]int64, error) {
	arr := []int64{}
	for i := int64(0); i < count; i++ {
		arr = append(arr, num)
	}
	return arr, nil
}

// simply returns p.personId
//
// we use this to test the '"'"'[optional]'"'"' enforcement, 
// as we invoke it with a null email
func (a AImpl'"$suffix"') PutPerson('"$context_arg"'p Person) (string, error) {
	return p.PersonId, nil
}

type BImpl'"$suffix"' struct{}

func (b BImpl'"$suffix"') Echo('"$context_arg"'s string) (*string, error) {
	if s == "return-null" {
		return nil, nil
	}
	return &s, nil
}
' >> $server_file
}

case $context in
yes)
	generate_impls '' 'ctx context.Context'
	;;
no)
	generate_impls
	;;
both)
	generate_impls
	generate_impls 'WithContext' 'ctx context.Context'
	;;
esac

echo 'func main() {
	flag.Parse()
	idlFile := flag.Arg(0)

	idl, err := barrister.ParseIdlJsonFile(idlFile)
	if err != nil {
		panic(err)
	}
' >> $server_file

function generate_server {
	local suffix=$1
echo '
	srv'"$suffix"' := NewJSONServer'"$suffix"'(idl, true, AImpl'"$suffix"'{}, BImpl'"$suffix"'{})
	http.Handle("/'"$suffix"'", &srv'"$suffix"')
' >> $server_file
}

case $context in
yes|no)
	generate_server
	;;
both)
	generate_server
	generate_server 'WithContext'
	;;
esac

echo '
	err = http.ListenAndServe(":9233", nil)
	if err != nil {
		panic(err)
	}
}' >> $server_file
