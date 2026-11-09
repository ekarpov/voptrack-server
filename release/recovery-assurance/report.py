"""Fail-closed local evidence aggregation; no provider or custody authority."""
import json,pathlib
from provenance import read_json,verify_provenance

import sys as _sys
from pathlib import Path as _Path
_sys.path.insert(0, str(_Path(__file__).resolve().parents[2] / 'ci'))
from tessera_pin import expected_pin
PIN = expected_pin()
def build_report(root,image,home_mode='public'):
 if home_mode not in ('public','private','off'):raise ValueError('unsupported Home mode')
 root=pathlib.Path(root)
 def read(path):return read_json(root/path)
 def require(value,label):
  if not value:raise ValueError(label)
 off=read('off-machine/result.json')
 require(off.get('passed') is True and off.get('image')==image and off.get('mode')=='assurance','destination gate missing or mismatched')
 require(off.get('local_synthetic_only') is True and off.get('aws_qualified') is False and off.get('separate_host_qualified') is False,'local receipt claims external qualification')
 guided=read('off-machine/guided/result.json')
 require(guided.get('passed') is True and guided.get('image')==image and guided.get('independent_host') is False,'guided gate missing or mismatched')
 products={}
 for variant in ('household','research'):
  source=read('source-'+variant+'/result.json')
  require(source.get('variant')==variant and source.get('entry')==('protected-home-setup' if variant=='household' else 'operator-and-study-wizard'),'source entry/variant mismatch')
  require(source.get('image')==image and all(source.get(k) is True for k in ('native_artifacts','real_collector_outbox','host_approved_committed_kit','source_services_unavailable')) and source.get('source_volumes_preserved',0)>0,'fresh source evidence missing')
  if variant=='household':require(source.get('home_mode','public')==home_mode,'source Home mode mismatch')
  copy=read('off-machine/success-'+variant+'/copy-result.json')
  require(copy.get('variant')==variant and copy.get('real_collector_outbox') is True and copy.get('fixture_objects_read_back') is True and copy.get('separate_read_only_fetch') is True,'real outbox/readback/fetch not established')
  check=read('off-machine/guided/success-'+variant+'/correctness.json')
  require(check.get('variant')==variant and all(check.get(k) is True for k in ('actual_guided_target_verified','source_services_unavailable','record_isolation','later_erasure_exclusion','producer_epoch_settings')),'restored correctness missing')
  require(check.get('saved_console_state') is (variant!='household' or home_mode!='off'),'Console state expectation mismatch')
  if variant=='household':
   if home_mode=='off':require(check.get('app_only_authority_absent') is True,'app-only authority proof missing')
   home=read('off-machine/guided/success-household/verification-inputs/home-assurance-receipt.json')
   require(home.get('home_mode','public')==home_mode,'restored Home mode mismatch')
   if home_mode!='public':require(home.get('mode_authority_and_routing_preserved') is True,'Home mode authority/routing proof missing')
   require(all(home.get(k) is True for k in ('fixed_choices_restored','configuration_matches','password_retirement_preserved')),'Home setup recovery missing')
  if variant=='research':
   extra=read('off-machine/guided/success-research/verification-inputs/research-assurance-receipt.json')
   require(all(extra.get(k) is True for k in ('study_document_restored','consent_restored','participant_records_restored','participant_isolation')),'Research study acceptance missing')
  verify_provenance(root,variant)
  products[variant]={'source_outbox_fetch_binding':'verified','local_integration':'verified','entry':source['entry'],'source_services':'unavailable-during-recovery','source_targets':'preserved','transport':'local-tls-fixture','aws_iam':'pending-owner-approved-destination','separate_host_recovery':'pending-owner-approved-host','physical_custody':'unverified','settings_freshness':'selected-export-only; current-source-unverified','copy_seconds':copy['copy_seconds'],'home_mode':home_mode if variant=='household' else None,'console_state':'not-applicable-app-only' if variant=='household' and home_mode=='off' else 'verified'}
 return {'version':1,'image':image,'pin':PIN,'products':products,'deployment_performed':False,'remote_deletion':'disabled','loss_window':'no-universal-bound; production/collection/outages and independent erasure-evidence gaps remain','scope':'local synthetic integration only'}
