import {Card,CardContent,CardHeader,CardTitle} from '@/components/ui/card';
import {ErrorState,Loading} from '@/components/Shared';
import {useHarnesses} from '@/services/devices';
import type {User} from '@/lib/types';
export function Harnesses(_: {me:User}) {const q=useHarnesses();return <Card><CardHeader><CardTitle>设备上报的执行器</CardTitle></CardHeader><CardContent>{q.isPending?<Loading/>:q.isError?<ErrorState error={q.error}/>:<ul className="grid gap-2 text-sm">{q.data.map(h=><li key={h.name}>{h.name}</li>)}</ul>}</CardContent></Card>;}
