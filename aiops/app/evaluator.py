"""Called only with a frozen result; no model invocation here."""
FIELDS=("root_service","root_component","cause_code","impact_class")

def evaluate(analysis, truth):
    if analysis["status"]!="completed" or not analysis.get("result"):
        return {"status":"unscorable","reason":"no frozen diagnosis"}
    if not truth["valid"]:
        return {"status":"unscorable","reason":truth["reason"]}
    expected=truth["intended"]
    checks={k:{"expected":expected[k],"actual":analysis["result"][k],"matched":expected[k]==analysis["result"][k]} for k in FIELDS}
    matches=sum(v["matched"] for v in checks.values())
    return {"status":"matched" if matches==4 else "partial" if matches else "mismatch","dimensions":checks,"semantic_review":"not performed; matching fields and valid references do not prove narrative correctness"}

def summary(runs, analyses, truth):
    groups={}
    for source in ("http","external","fixture"):
        rows=[a for a in analyses if a.get("source")==source]
        scored=[(a,evaluate(a,truth[a["run_id"]])) for a in rows]
        valid=[(a,e) for a,e in scored if e["status"]!="unscorable"]
        matched=sum(e["status"]=="matched" for a,e in valid)
        healthy=[(a,e) for a,e in valid if truth[a["run_id"]]["intended"]["root_service"]=="none"]
        groups[source]={"total_analyses":len(rows),"completed":sum(a["status"]=="completed" for a in rows),"scorable":len(valid),"matched":matched,"match_rate":matched/len(valid) if valid else None,"healthy_false_positives":sum(a["result"]["status"]=="diagnosed" for a,e in healthy),"healthy_denominator":len(healthy),"excluded":[{"analysis_id":a["id"],"reason":e.get("reason")} for a,e in scored if e["status"]=="unscorable"],"inconclusive":sum(a.get("result",{}).get("status")=="inconclusive" for a in rows)}
        bucket=groups[source]
        bucket["end_to_end_matched_runs"]=len({a["run_id"] for a,e in valid if e["status"]=="matched"})
        bucket["end_to_end_denominator"]=len(runs)
        durations=[a["elapsed_seconds"] for a in rows if a.get("elapsed_seconds") is not None]
        bucket["mean_diagnosis_seconds"]=sum(durations)/len(durations) if durations else None
        bucket["by_view"]={}
        bucket["by_scenario"]={}
        for a,e in scored:
            for field,key in (("by_view",a.get("mode","all")),("by_scenario",truth[a["run_id"]]["intended"].get("scenario_id","unknown"))):
                counts=bucket[field].setdefault(key,{"matched":0,"partial":0,"mismatch":0,"unscorable":0,"total":0})
                counts[e["status"]]+=1;counts["total"]+=1
        rates=[]
        for counts in bucket["by_scenario"].values():
            denom=counts["total"]-counts["unscorable"]
            if denom:rates.append(counts["matched"]/denom)
        bucket["scenario_macro_match_rate"]=sum(rates)/len(rates) if rates else None
    return {"total_runs":len(runs),"valid_injections":sum(t["valid"] for t in truth.values()),"evidence_ready":sum(r.get("evidence_ready",False) for r in runs),"three_signals_usable":sum(all(r.get("signals",{}).get(s,{}).get("status") in ("available","partial") for s in ("traces","logs","metrics")) for r in runs),"groups":groups,"limitations":["HTTP, external and fixture sources are separate; fixture is never real-model accuracy", "Ablations are correlated observations, not independent trials"]}
