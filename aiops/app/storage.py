import json
import sqlite3
import time

class Store:
    def __init__(self, path):
        self.db=sqlite3.connect(path,check_same_thread=False)
        self.db.execute("PRAGMA journal_mode=WAL")
        self.db.execute("CREATE TABLE IF NOT EXISTS records(kind TEXT,id TEXT,run_id TEXT,created REAL,data TEXT,PRIMARY KEY(kind,id))")
        self.db.commit()
        for kind in ("run","analysis"):
            for row in self.list(kind):
                if kind=="run" and row.get("analysis_state")=="collecting":
                    row["analysis_state"]="interrupted"
                    self.put(kind,row["id"],row)
                terminal=("completed","cancelled","interrupted","environment_unhealthy","injection_failed") if kind=="run" else ("completed","failed","awaiting_external","interrupted")
                if row["status"] not in terminal:
                    row["status"]="interrupted";self.put(kind,row["id"],row,row.get("run_id",row["id"]))

    def put(self,kind,id,data,run_id=None):
        with self.db:
            self.db.execute("INSERT INTO records VALUES(?,?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data",(kind,id,run_id or id,time.time(),json.dumps(data)))

    def get(self,kind,id):
        row=self.db.execute("SELECT data FROM records WHERE kind=? AND id=?",(kind,id)).fetchone()
        if row is None:raise KeyError(id)
        return json.loads(row[0])

    def list(self,kind):
        return [json.loads(r[0]) for r in self.db.execute("SELECT data FROM records WHERE kind=? ORDER BY created DESC",(kind,))]

    def prune(self):
        # Only terminal local records. Never remove an active run or unrelated filesystem data.
        rows=self.db.execute("SELECT id,created,data FROM records WHERE kind='run' ORDER BY created DESC").fetchall()
        for i,(id,created,data) in enumerate(rows):
            if (i>=500 or created<time.time()-7*86400) and json.loads(data)["status"] in ("completed","cancelled","interrupted","environment_unhealthy","injection_failed"):
                with self.db:self.db.execute("DELETE FROM records WHERE run_id=?",(id,))
