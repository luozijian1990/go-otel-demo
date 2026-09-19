"""Analyzer boundary: these types have no experiment controls or oracle fields."""
from typing import Literal
from pydantic import BaseModel, ConfigDict, Field
import hashlib
import json

class Strict(BaseModel):
    model_config = ConfigDict(extra="forbid")

class IncidentContext(Strict):
    time_unit: Literal["unix_seconds"] = "unix_seconds"
    run_id: str
    entry_service: Literal["service-a"] = "service-a"
    route: Literal["/exercise"] = "/exercise"
    start_at: float
    end_at: float
    request_ids: list[str] = Field(default_factory=list)
    trace_ids: list[str] = Field(default_factory=list)
    observed_symptoms: list[dict] = Field(default_factory=list)
    windows: dict[str, tuple[float, float]] = Field(default_factory=dict)
    completed_requests: int = 0

class SignalState(Strict):
    status: Literal["available", "partial", "unavailable", "not_found_after_deadline", "insufficient_samples", "pending"]
    reason: str | None = None
    attempts: int = 0
    truncated: bool = False

class EvidenceBundle(Strict):
    schema_version: Literal["1.0"] = "1.0"
    run_id: str
    incident: IncidentContext
    availability: dict[str, SignalState]
    traces: list[dict] = Field(default_factory=list)
    logs: list[dict] = Field(default_factory=list)
    metrics: dict = Field(default_factory=dict)
    evidence_index: dict[str, dict] = Field(default_factory=dict)
    limitations: list[str] = Field(default_factory=list)
    collected_at: float
    bundle_hash: str = ""

class DiagnosisResult(Strict):
    schema_version: Literal["1.0"] = "1.0"
    evidence_bundle_hash: str
    status: Literal["diagnosed", "inconclusive", "no_anomaly"]
    summary: str = Field(max_length=6000)
    root_service: Literal["service-a", "service-b", "service-c", "service-d", "traefik", "none", "unknown"]
    root_component: Literal["application", "mysql", "redis_client", "http_client", "none", "unknown"]
    cause_code: Literal["application_error", "table_not_found", "connection_refused", "application_delay", "slow_query", "none", "unknown"]
    impact_class: Literal["failed", "slow", "degraded", "none", "unknown"]
    confidence: Literal["high", "medium", "low"]
    root_evidence_ids: list[str] = Field(max_length=50)
    propagation: list[str] = Field(default_factory=list, max_length=20)
    supporting_findings: list[str] = Field(default_factory=list, max_length=30)
    counter_evidence: list[str] = Field(default_factory=list, max_length=30)
    limitations: list[str] = Field(default_factory=list, max_length=30)
    hypotheses: list[str] = Field(default_factory=list, max_length=20)
    recommended_checks: list[str] = Field(default_factory=list, max_length=20)

def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()).hexdigest()

def validate_result(value, bundle):
    result = DiagnosisResult.model_validate(value)
    if result.evidence_bundle_hash != bundle.bundle_hash:
        raise ValueError("evidence hash mismatch")
    if set(result.root_evidence_ids) - bundle.evidence_index.keys():
        raise ValueError("unknown evidence reference")
    if result.status == "diagnosed" and not result.root_evidence_ids:
        raise ValueError("diagnosed requires evidence")
    if result.status == "no_anomaly" and (result.root_service, result.root_component, result.cause_code, result.impact_class) != ("none",)*4:
        raise ValueError("inconsistent no_anomaly result")
    return result
