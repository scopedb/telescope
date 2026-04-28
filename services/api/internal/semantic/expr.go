/*
 * Copyright 2026 ScopeDB, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package semantic

import (
	"errors"
	"fmt"
	"strings"

	"github.com/scopedb/telescope/services/api/internal/scopeql"
)

type Expr interface {
	exprNode()
	ScopeQL() string
	Validate() error
}

type RefExpr struct {
	Name string
}

func Ref(name string) Expr {
	return RefExpr{Name: name}
}

func (RefExpr) exprNode() {}

func (e RefExpr) ScopeQL() string {
	return scopeql.QuoteIdentifier(e.Name)
}

func (e RefExpr) Validate() error {
	if strings.TrimSpace(e.Name) == "" {
		return errors.New("ref expression name is required")
	}
	return nil
}

type CallExpr struct {
	Name string
	Args []Expr
}

func Call(name string, args ...Expr) Expr {
	return CallExpr{Name: name, Args: args}
}

func (CallExpr) exprNode() {}

func (e CallExpr) ScopeQL() string {
	args := make([]string, 0, len(e.Args))
	for _, arg := range e.Args {
		args = append(args, arg.ScopeQL())
	}
	return fmt.Sprintf("%s(%s)", e.Name, strings.Join(args, ", "))
}

func (e CallExpr) Validate() error {
	var errs []error

	if strings.TrimSpace(e.Name) == "" {
		errs = append(errs, errors.New("call expression name is required"))
	}
	if len(e.Args) == 0 {
		errs = append(errs, fmt.Errorf("call expression %q requires at least one argument", e.Name))
	}
	for i, arg := range e.Args {
		if arg == nil {
			errs = append(errs, fmt.Errorf("call expression %q argument %d is nil", e.Name, i))
			continue
		}
		if err := arg.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("call expression %q argument %d: %w", e.Name, i, err))
		}
	}

	return errors.Join(errs...)
}
