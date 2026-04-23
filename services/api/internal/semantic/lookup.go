package semantic

func (r Registry) Field(name string) (FieldSpec, bool) {
	for _, field := range r.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return FieldSpec{}, false
}

func (r Registry) Relation(name string) (RelationSpec, bool) {
	for _, relation := range r.Relations {
		if relation.Name == name {
			return relation, true
		}
	}
	return RelationSpec{}, false
}

func (r Registry) Intent(name string) (IntentSpec, bool) {
	for _, intent := range r.Intents {
		if intent.Name == name {
			return intent, true
		}
	}
	return IntentSpec{}, false
}
