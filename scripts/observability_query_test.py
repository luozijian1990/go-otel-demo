import importlib.util
import json
from pathlib import Path
import sys
import unittest
from unittest.mock import patch
from argparse import Namespace

ROOT=Path(__file__).parent/'observability'
sys.path.insert(0,str(ROOT))
spec=importlib.util.spec_from_file_location('query',ROOT/'query.py');q=importlib.util.module_from_spec(spec);spec.loader.exec_module(q)

class QueryTest(unittest.TestCase):
    def test_go_nanosecond_timestamp_on_python39(self):
        self.assertEqual(q.epoch('2026-09-19T08:17:50.839245293Z'),q.epoch('2026-09-19T08:17:50.839245Z'))
        self.assertEqual(q.epoch('2026-09-19T08:17:50.8Z'),q.epoch('2026-09-19T08:17:50.800000Z'))

    def test_secret_scrub_preserves_native_error(self):
        data=q.scrub({'X-Demo-Fault-Token':'hidden','scenario_id':'answer','message':'Error 1146: table not found','http.request.header.authorization':'secret','trace_id':'abc'})
        self.assertEqual(data,{'message':'Error 1146: table not found','trace_id':'abc'})

    def test_loki_exact_filter_and_units(self):
        a=Namespace(service='order-service',trace_id='a'*32,request_id=None,run_id=None,limit=100,url=None)
        raw={'data':{'result':[{'stream':{'service_name':'order-service'},'values':[['1000000000','business failure',{'trace_id':'a'*32}]]}]}}
        with patch.object(q,'read_json',return_value=raw) as read:
            result=q.logs(a,1,2)
        self.assertEqual(read.call_args.args[2]['start'],'1000000000')
        self.assertIn('trace_id=',read.call_args.args[2]['query'])
        self.assertEqual(result['evidence'][0]['data']['correlation'],'exact_trace_id')

    def test_metrics_no_samples_not_zero(self):
        a=Namespace(service='payment-service',url=None)
        with patch.object(q,'read_json',return_value={'status':'success','data':{'result':[]}}):result=q.metrics(a,1,61)
        self.assertEqual(result['status'],'no_samples')
        self.assertTrue(all(not x['data']['series'] for x in result['evidence']))

    def test_trace_mismatch_rejected(self):
        a=Namespace(trace_id='a'*32,service=None,url=None,wait=0)
        with patch.object(q,'read_json',return_value={'data':[{'traceID':'b'*32}]}),self.assertRaises(ValueError):q.traces(a,1,2)

    def test_helpers_do_not_import_controls(self):
        import ast
        tree=ast.parse((ROOT/'query.py').read_text())
        imports=[n.module or '' for n in ast.walk(tree) if isinstance(n,ast.ImportFrom)]
        self.assertFalse(any('aiops' in x or 'commerce' in x for x in imports))

if __name__=='__main__':unittest.main()

class GatewayTest(unittest.TestCase):
    def test_default_logs_include_gateway_and_keep_and_filters(self):
        a=Namespace(service=None,trace_id='a'*32,request_id='req',run_id='run',limit=100,url=None)
        with patch.object(q,'read_json',return_value={}) as read:
            result=q.logs(a,1,2)
        expression=read.call_args.args[2]['query']
        self.assertIn('traefik',expression)
        self.assertIn(' | request_id=',expression)
        self.assertIn(' | experiment_id=',expression)
        self.assertTrue(any('Traefik' in x and 'AND' in x for x in result['limitations']))
    def test_gateway_metrics_rejected_before_network(self):
        with patch.object(q,'read_json') as read:
            with self.assertRaisesRegex(ValueError,'business metric'):q.metrics(Namespace(service='traefik',url=None),1,61)
            read.assert_not_called()
    def test_unknown_service_rejected(self):
        for source in ('logs','traces','metrics'):
            with self.assertRaises(ValueError):q.validate_service(source,'unknown')
        for source in ('logs','traces'):q.validate_service(source,'traefik')
