import { read, utils } from 'xlsx';
export interface SheetPreview { name:string; rows:{number:number;cells:{text:string;formula?:string}[]}[]; columns:string[]; truncated:boolean }
self.onmessage = (event:MessageEvent<ArrayBuffer>) => {
 try {
  const workbook=read(event.data,{type:'array',sheetRows:201,cellFormula:true,cellHTML:false,bookVBA:false});
  const sheets:SheetPreview[]=workbook.SheetNames.slice(0,50).map(name=>{
   const sheet=workbook.Sheets[name];const range=utils.decode_range(sheet['!ref']||'A1');const full=utils.decode_range(sheet['!fullref']||sheet['!ref']||'A1');
   const lastRow=Math.min(range.e.r,range.s.r+200),lastCol=Math.min(range.e.c,range.s.c+29);
   const rows:SheetPreview['rows']=[];
   for(let r=range.s.r;r<=lastRow;r++){
    const cells:SheetPreview['rows'][number]['cells']=[];
    for(let c=range.s.c;c<=lastCol;c++){
     const cell=sheet[utils.encode_cell({r,c})];
     cells.push({text:cell?.v===undefined?(cell?.f?'未保存缓存结果':''):utils.format_cell(cell),...(cell?.f?{formula:String(cell.f)}:{})});
    }
    rows.push({number:r+1,cells});
   }
   return {name,rows,columns:Array.from({length:lastCol-range.s.c+1},(_,i)=>utils.encode_col(range.s.c+i)),truncated:full.e.r>lastRow||full.e.c>lastCol};
  });
  self.postMessage({sheets,sheetsTruncated:workbook.SheetNames.length>50});
 }catch{self.postMessage({error:'无法解析此工作簿，请下载后用表格软件打开。'});}
};
