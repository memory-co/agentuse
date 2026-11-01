"""Validate events against this independent package's event.schema.json.

The contract uses a small JSON Schema 2020-12 vocabulary, implemented with the
standard library to avoid requiring jsonschema or a shared package at runtime.
"""
import datetime
import json
from pathlib import Path
import re
import uuid

SCHEMA = json.loads((Path(__file__).resolve().parent.parent/'event.schema.json').read_text())


def _validate(value, schema, path='$'):
    if '$ref' in schema:
        return _validate(value, SCHEMA['$defs'][schema['$ref'].removeprefix('#/$defs/')], path)
    if 'oneOf' in schema:
        matches = 0
        failures = []
        for branch in schema['oneOf']:
            try:
                _validate(value, branch, path)
                matches += 1
            except ValueError as error:
                failures.append(str(error))
        if matches != 1:
            # Give a useful explanation for known event tags, rather than an
            # unrelated error from a different event variant.
            candidates = [branch for branch in schema['oneOf'] if isinstance(value,dict) and '$ref' in branch
                          and SCHEMA['$defs'][branch['$ref'].split('/')[-1]].get('properties',{}).get('type',{}).get('const') == value.get('type')]
            if len(candidates) == 1:
                _validate(value, candidates[0], path)
            raise ValueError(f'{path}: event must match exactly one documented format ({matches} matched)')
    if 'const' in schema and value != schema['const']:
        raise ValueError(f'{path}: expected {schema["const"]!r}')
    if 'enum' in schema and value not in schema['enum']:
        raise ValueError(f'{path}: invalid enum value {value!r}')
    kind = schema.get('type')
    kinds = kind if isinstance(kind,list) else [kind] if kind else []
    types = {'object':isinstance(value,dict), 'array':isinstance(value,list),
             'string':isinstance(value,str), 'boolean':isinstance(value,bool),
             'number':isinstance(value,(int,float)) and not isinstance(value,bool),
             'integer':isinstance(value,int) and not isinstance(value,bool),
             'null':value is None}
    if kinds and not any(types[name] for name in kinds):
        raise ValueError(f'{path}: expected {kind}')
    if isinstance(value,dict):
        missing = set(schema.get('required',[]))-value.keys()
        if missing:
            raise ValueError(f'{path}: missing required fields {sorted(missing)}')
        if len(value)<schema.get('minProperties',0):
            raise ValueError(f'{path}: too few properties')
        properties = schema.get('properties',{})
        if schema.get('additionalProperties') is False and value.keys()-properties.keys():
            raise ValueError(f'{path}: undocumented fields {sorted(value.keys()-properties.keys())}')
        if isinstance(schema.get('additionalProperties'),dict):
            for key in value.keys()-properties.keys():
                _validate(value[key],schema['additionalProperties'],path+'.'+key)
        for key,child in properties.items():
            if key in value:
                _validate(value[key],child,path+'.'+key)
    if isinstance(value,list):
        if len(value)<schema.get('minItems',0):
            raise ValueError(f'{path}: too few items')
        if schema.get('uniqueItems') and len({json.dumps(item,sort_keys=True) for item in value}) != len(value):
            raise ValueError(f'{path}: duplicate items')
        for index,child in enumerate(value):
            if 'items' in schema:
                _validate(child,schema['items'],f'{path}[{index}]')
    if isinstance(value,str):
        if len(value)<schema.get('minLength',0):
            raise ValueError(f'{path}: string is too short')
        if schema.get('format') == 'uuid':
            try:
                if str(uuid.UUID(value)) != value:
                    raise ValueError('not canonical')
            except (ValueError, AttributeError) as error:
                raise ValueError(f'{path}: expected canonical UUID') from error
        if schema.get('format') == 'date-time':
            if not re.fullmatch(r'\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|\+00:00)',value):
                raise ValueError(f'{path}: expected UTC RFC 3339 timestamp')
            try:
                datetime.datetime.fromisoformat(value.replace('Z','+00:00'))
            except ValueError as error:
                raise ValueError(f'{path}: invalid timestamp') from error
    if isinstance(value,(int,float)) and not isinstance(value,bool) and 'minimum' in schema:
        if value<schema['minimum']:
            raise ValueError(f'{path}: below minimum')


def validate_event(event):
    """Raise ValueError before any file write for malformed or unknown events."""
    # Also rejects non-JSON payloads and NaN/Infinity anywhere in native data.
    try:
        json.dumps(event,ensure_ascii=False,allow_nan=False)
    except (TypeError, ValueError) as error:
        raise ValueError('event is not valid JSON: '+str(error)) from error
    _validate(event,SCHEMA)


def tool_status(value, default='unknown'):
    if value is None:
        return default
    return {'inProgress':'running','in_progress':'running','running':'running',
            'completed':'completed','failed':'failed','declined':'failed',
            'interrupted':'interrupted','cancelled':'interrupted'}.get(value,'unknown')


def plan_steps(items):
    return [dict(text=item.get('step',''),status={'pending':'pending','inProgress':'in_progress',
                 'in_progress':'in_progress','completed':'completed'}.get(item.get('status'),'unknown')) for item in items]
