import concurrent.futures,json,urllib.request,pathlib
urls={
'gmail':'https://gmail.googleapis.com/$discovery/rest?version=v1',
'drive':'https://www.googleapis.com/discovery/v1/apis/drive/v3/rest',
'docs':'https://docs.googleapis.com/$discovery/rest?version=v1',
'sheets':'https://sheets.googleapis.com/$discovery/rest?version=v4',
'slides':'https://slides.googleapis.com/$discovery/rest?version=v1',
'calendar':'https://www.googleapis.com/discovery/v1/apis/calendar/v3/rest',
'people':'https://people.googleapis.com/$discovery/rest?version=v1',
 'tasks':'https://tasks.googleapis.com/$discovery/rest?version=v1',
'meet':'https://meet.googleapis.com/$discovery/rest?version=v2'}
selections={
'gmail': [('list-messages','users.messages.list'),('get-message','users.messages.get'),('get-attachment','users.messages.attachments.get'),('list-threads','users.threads.list'),('get-thread','users.threads.get'),('list-labels','users.labels.list'),('create-draft','users.drafts.create'),('get-draft','users.drafts.get'),('update-draft','users.drafts.update'),('send-draft','users.drafts.send'),('modify-message','users.messages.modify'),('trash-message','users.messages.trash')],
'drive':[('list-files','files.list'),('get-file','files.get'),('create-file','files.create'),('update-file','files.update'),('copy-file','files.copy'),('list-permissions','permissions.list'),('create-permission','permissions.create'),('export-file','files.export')],
'docs':[('get-document','documents.get'),('create-document','documents.create'),('batch-update-document','documents.batchUpdate')],
'sheets':[('get-spreadsheet','spreadsheets.get'),('create-spreadsheet','spreadsheets.create'),('get-values','spreadsheets.values.get'),('update-values','spreadsheets.values.update'),('append-values','spreadsheets.values.append'),('batch-update-spreadsheet','spreadsheets.batchUpdate')],
'slides':[('get-presentation','presentations.get'),('create-presentation','presentations.create'),('batch-update-presentation','presentations.batchUpdate')],
'calendar':[('list-calendars','calendarList.list'),('list-events','events.list'),('get-event','events.get'),('create-event','events.insert'),('update-event','events.patch'),('delete-event','events.delete')],
'people':[('list-contacts','people.connections.list'),('search-contacts','people.searchContacts'),('get-contact','people.get'),('create-contact','people.createContact'),('update-contact','people.updateContact'),('delete-contact','people.deleteContact')],
'tasks':[('list-tasklists','tasklists.list'),('create-tasklist','tasklists.insert'),('list-tasks','tasks.list'),('get-task','tasks.get'),('create-task','tasks.insert'),('update-task','tasks.patch'),('delete-task','tasks.delete')],
'meet':[('create-space','spaces.create'),('get-space','spaces.get'),('update-space','spaces.patch'),('list-conferences','conferenceRecords.list'),('get-conference','conferenceRecords.get'),('list-participants','conferenceRecords.participants.list'),('list-recordings','conferenceRecords.recordings.list'),('list-transcripts','conferenceRecords.transcripts.list'),('list-transcript-entries','conferenceRecords.transcripts.entries.list')]}
scopes={'gmail':['gmail.modify'],'drive':['drive'],'docs':['documents'],'sheets':['spreadsheets'],'slides':['presentations'],'calendar':['calendar.events'],'people':['contacts'],'tasks':['tasks'],'meet':['meetings.space.readonly']}

def response_schema(doc, schema, depth=0, seen=()):
 # Keep bounded response contracts, not the entire recursively linked API.
 ref=schema.get('$ref')
 if ref:
  if ref in seen or depth>3:return {'type':'object','additionalProperties':True}
  schema={**doc['schemas'][ref],**{k:v for k,v in schema.items() if k!='$ref'}}
  seen=(*seen,ref)
 result={k:schema[k] for k in ['type','description','enum'] if k in schema}
 if schema.get('type')=='object':
  result['additionalProperties']=True
  if depth<=3:
   result['properties']={k:response_schema(doc,v,depth+1,seen) for k,v in sorted(schema.get('properties',{}).items())}
 if schema.get('type')=='array':result['items']=response_schema(doc,schema.get('items',{}),depth,seen)
 return result

def read(item):
 service,url=item
 with urllib.request.urlopen(url,timeout=40) as r:return service,json.load(r)
docs=dict(concurrent.futures.ThreadPoolExecutor(max_workers=9).map(read,urls.items()))
operations=[]
for service, selected in selections.items():
 doc=docs[service]
 for slug,method in selected:
  obj=doc
  for resource in method.split('.')[:-1]: obj=obj['resources'][resource]
  m=obj['methods'][method.split('.')[-1]]
  params={}
  for key,value in sorted(m.get('parameters',{}).items()):
   if key in ['userId']:continue
   if value.get('location') not in ['path','query']:continue
   if value.get('deprecated'):continue
   # No delegated identities or provider transport overrides.
   if key in ['quotaUser','key','oauth_token','access_token','alt','uploadType','upload_protocol']:continue
   params[key]={k:value[k] for k in ['type','required','enum','minimum','maximum','default','repeated','description'] if k in value}
   if value.get('enumDescriptions'):
    params[key]['description']=params[key].get('description','')+' '+ ' '.join(name+': '+text for name,text in zip(value.get('enum',[]),value['enumDescriptions']))
   params[key]['location']=value['location']
  for key in ['fields']:
   if key not in params:params[key]={'location':'query','type':'string','description':'Google partial response fields projection. Select fields from this operation\'s response schema; request parameters and metadata values are not response field names.'}
  scope=scopes[service]
  if service=='calendar' and slug=='list-calendars':scope=['calendar.calendarlist.readonly']
  if service=='meet' and slug in ['create-space','update-space']:scope=['meetings.space.created']
  risk='read' if m['httpMethod']=='GET' else ('destructive' if m['httpMethod']=='DELETE' or slug=='trash-message' else 'external' if slug in ['send-draft','create-permission','create-event','update-event'] else 'write')
  op={'name':'google-'+service+'-'+slug,'service':service,'method':m['httpMethod'],'baseURL':doc['rootUrl']+doc.get('servicePath',''),'path':m['path'],'description':slug.replace('-',' ').capitalize()+' in Google '+service.capitalize()+'.','params':params,'body':bool(m.get('request')),'scopes':['https://www.googleapis.com/auth/'+s for s in scope],'risk':risk,'documentation':doc.get('documentationLink',urls[service]),'responseFormat':'bytes' if slug=='export-file' else 'json'}
  if service=='gmail' and slug in ['list-messages','get-message']:
   op['responseSchema']=response_schema(doc,m['response'])
   op['description']=m['description']
   if slug=='list-messages':
    # Discovery reuses Message here but expressly limits list entries to IDs.
    # Do not advertise Message's other properties as available list data.
    messages=op['responseSchema']['properties']['messages']
    messages['items']['properties']={k:v for k,v in messages['items']['properties'].items() if k in ['id','threadId']}
    op['description']+=' '+messages['description']
   if slug=='get-message':
    op['description']+=' '+op['responseSchema']['properties']['payload']['properties']['headers']['description']
  operations.append(op)
for slug,path,method in [('send-email','gmail/v1/users/{userId}/messages/send','POST'),('draft-email','gmail/v1/users/{userId}/drafts','POST')]:
 operations.append({'name':'google-gmail-'+slug,'service':'gmail','method':method,'baseURL':'https://gmail.googleapis.com/','path':path,'description':slug.replace('-',' ').capitalize()+' with a plain-text body.','params':{},'body':False,'scopes':['https://www.googleapis.com/auth/gmail.modify'],'risk':'external' if slug=='send-email' else 'write','responseFormat':'json','bodyFormat':'email'})
for slug,base,method,params in [('upload-file','https://www.googleapis.com/upload/drive/v3/','POST',{}),('download-file','https://www.googleapis.com/drive/v3/','GET',{'fileId':{'type':'string','location':'path','required':True}})]:
 operations.append({'name':'google-drive-'+slug,'service':'drive','method':method,'baseURL':base,'path':'files' if slug=='upload-file' else 'files/{fileId}','description':slug.replace('-',' ').capitalize()+' with bounded file content.','params':params,'body':False,'scopes':['https://www.googleapis.com/auth/drive'],'risk':'write' if slug=='upload-file' else 'read','responseFormat':'bytes' if slug=='download-file' else 'json','bodyFormat':'multipart' if slug=='upload-file' else ''})
operations.append({'name':'google-drive-trash-file','service':'drive','method':'PATCH','baseURL':'https://www.googleapis.com/drive/v3/','path':'files/{fileId}','description':'Move an existing Drive file to trash.','params':{'fileId':{'type':'string','location':'path','required':True}},'body':False,'bodyFormat':'trash','scopes':['https://www.googleapis.com/auth/drive'],'risk':'destructive','responseFormat':'json'})
pathlib.Path('skills/google-workspace/operations.json').write_text(json.dumps(operations,indent=2)+'\n')
print('Generated',len(operations),'fixed Workspace operations from Google Discovery documents')
