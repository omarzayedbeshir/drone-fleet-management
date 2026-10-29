#!/usr/bin/env python3
"""Validate the OpenAPI draft, examples, and basic source/schema alignment.

Documentation-only dependencies live in backend/requirements-openapi.txt.
No API server, database, or network access is needed to run this script.
"""
from __future__ import annotations

import json
from pathlib import Path
import re
from urllib.parse import unquote, urldefrag, urlparse

from jsonschema import Draft202012Validator, FormatChecker
from openapi_spec_validator import validate
from referencing import Registry, Resource
from referencing.jsonschema import DRAFT202012
import yaml

ROOT = Path(__file__).resolve().parents[1]
SPEC_PATH = ROOT / "openapi.yaml"
METHODS = {"get", "post", "patch", "delete", "put", "head", "options"}


def load(path: Path):
    return json.loads(path.read_text()) if path.suffix == ".json" else yaml.safe_load(path.read_text())


def retrieve(uri: str) -> Resource:
    parsed = urlparse(uri)
    if parsed.scheme != "file":
        raise ValueError(f"Only local schema references are allowed: {uri}")
    path = Path(unquote(parsed.path)).resolve()
    if not path.is_relative_to(ROOT):
        raise ValueError("Schema references must stay inside backend/")
    return Resource.from_contents(load(path), default_specification=DRAFT202012)


def go_fields(filename: str, struct: str) -> set[str]:
    source = (ROOT / filename).read_text()
    match = re.search(rf"type {struct} struct\s*\{{([^}}]*)\}}", source, re.S)
    if not match:
        raise AssertionError(f"Missing Go DTO {struct}")
    return set(re.findall(r'json:"([^",]+)', match[1]))


def main() -> None:
    spec = load(SPEC_PATH)
    validate(spec, base_uri=SPEC_PATH.as_uri())
    registry = Registry(retrieve=retrieve).with_resource(
        SPEC_PATH.as_uri(), Resource.from_contents(spec, default_specification=DRAFT202012)
    )

    # Resolve all references, not just those exercised by an example.
    def check_refs(value, base_uri=SPEC_PATH.as_uri()):
        if isinstance(value, dict):
            if "$ref" in value:
                from urllib.parse import urljoin
                uri = urljoin(base_uri, value["$ref"])
                document_uri, fragment = urldefrag(uri)
                resource = registry.get_or_retrieve(document_uri).value
                resource.pointer(fragment, registry.resolver(document_uri))
            for item in value.values():
                check_refs(item, base_uri)
        elif isinstance(value, list):
            for item in value:
                check_refs(item, base_uri)
    check_refs(spec)

    documented = {(method.upper(), path) for path, item in spec["paths"].items() for method in item if method in METHODS}
    implemented = set()
    for path in (ROOT / "internal/httpapi").glob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        implemented.update(re.findall(r'mux\.HandleFunc\("(GET|POST|PATCH|DELETE|PUT|HEAD|OPTIONS) ([^\"]+)"', path.read_text()))
    assert documented == implemented, f"Route drift: missing={implemented-documented}, unimplemented={documented-implemented}"

    components = spec["components"]["schemas"]
    for name, filename, struct in [
        ("User", "internal/auth/auth.go", "User"),
        ("Drone", "internal/drones/drones.go", "Drone"),
        ("DroneCreate", "internal/drones/drones.go", "Metadata"),
    ]:
        assert set(components[name]["properties"]) == go_fields(filename, struct), f"{name} fields drifted from Go DTO"
    assert set(components["DronePatch"]["properties"]) == set(components["DroneCreate"]["properties"])
    sample_fields = go_fields("internal/telemetry/telemetry.go", "Sample")
    stored_fields = sample_fields | go_fields("internal/telemetry/telemetry.go", "Stored")
    assert set(components["TelemetrySample"]["properties"]) == stored_fields, "Stored telemetry DTO drift"
    assert set(load(ROOT / "schemas/telemetry.schema.json")["properties"]) == sample_fields, "Common telemetry schema drift"
    assert components["NormalizedTelemetry"]["$ref"] == "./schemas/telemetry.schema.json", "Use the shared input schema"

    example_count = 0
    def validate_examples(value, pointer=""):
        nonlocal example_count
        if isinstance(value, dict):
            if "schema" in value and "example" in value:
                Draft202012Validator(
                    {"$ref": SPEC_PATH.as_uri() + "#" + pointer + "/schema"}, registry=registry, format_checker=FormatChecker()
                ).validate(value["example"])
                example_count += 1
            for key, item in value.items():
                escaped = str(key).replace("~", "~0").replace("/", "~1")
                validate_examples(item, pointer + "/" + escaped)
        elif isinstance(value, list):
            for index, item in enumerate(value):
                validate_examples(item, pointer + "/" + str(index))
    validate_examples(spec)

    ids = []
    origin_ref = {"$ref": "#/components/parameters/Origin"}
    for path, item in spec["paths"].items():
        for method, op in item.items():
            if method not in METHODS:
                continue
            ids.append(op["operationId"])
            assert op["x-implementation-status"] == "implemented"
            assert op["x-allowed-roles"], f"Missing role documentation: {method} {path}"
            if method in {"post", "patch", "delete", "put"}:
                assert origin_ref in op.get("parameters", []), f"Missing Origin protection: {method} {path}"
            for status, response in op["responses"].items():
                if status == "204":
                    assert "content" not in response, "204 responses must not have a body"
    assert len(ids) == len(set(ids)), "Operation IDs must be unique"
    print(f"OpenAPI valid: {len(spec['paths'])} paths, {len(ids)} operations, {example_count} schema-checked examples; routes/DTOs/telemetry schema aligned.")


if __name__ == "__main__":
    main()
