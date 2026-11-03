import {useQuery} from '@tanstack/react-query';
import {Card,CardContent,CardHeader,CardTitle} from '@/components/ui/card';
import {ErrorState,Loading} from '@/components/Shared';
import {api} from '@/lib/api';
import type {Harness,User} from '@/lib/types';
export const useHarnesses=()=>useQuery({queryKey:['harnesses'],queryFn:()=>api<Harness[]>('/api/harnesses'),staleTime:30000});
export function Harnesses(_: {me:User}) {const q=useHarnesses();return <Card><CardHeader><CardTitle>设备上报的执行器</CardTitle></CardHeader><CardContent>{q.isPending?<Loading/>:q.isError?<ErrorState error={q.error}/>:<ul className="grid gap-2 text-sm">{q.data.map(h=><li key={h.name}>{h.name}</li>)}</ul>}</CardContent></Card>;}
