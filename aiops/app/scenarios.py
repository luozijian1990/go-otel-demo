"""Operator-only catalogue. Never imported by evidence or analyzer."""
from dataclasses import dataclass

@dataclass(frozen=True)
class ExperimentSpec:
    scenario_id: str
    target: str
    action: str
    root_component: str
    cause_code: str
    impact_class: str
    version: str = "1"

CATALOGUE = [
    ExperimentSpec("app_error_a","service-a","app_error_a","application","application_error","failed"),
    ExperimentSpec("app_error_b","service-b","app_error_b","application","application_error","failed"),
    ExperimentSpec("mysql_missing_table_d","service-d","missing_table","mysql","table_not_found","failed"),
    ExperimentSpec("redis_connect_refused_c","service-c","connect_refused","redis_client","connection_refused","failed"),
    ExperimentSpec("application_delay_c","service-c","application_delay","application","application_delay","slow"),
    ExperimentSpec("mysql_slow_query_d","service-d","slow_query","mysql","slow_query","slow"),
    ExperimentSpec("redis_degraded_c","service-c","degraded","redis_client","connection_refused","degraded"),
]
HEALTHY=ExperimentSpec("healthy","none","","none","none","none")

@dataclass
class GroundTruth:
    intended: dict
    receipts: list
    valid: bool = False
    reason: str = "not executed"
